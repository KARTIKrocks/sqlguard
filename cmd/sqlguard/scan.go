package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/reporter"
	"github.com/spf13/cobra"
	"golang.org/x/tools/go/packages"
)

// SQL method names we look for on any receiver.
var sqlMethods = map[string]bool{
	"Query":           true,
	"QueryContext":    true,
	"QueryRow":        true,
	"QueryRowContext": true,
	"Exec":            true,
	"ExecContext":     true,
	"Prepare":         true,
	"PrepareContext":  true,
}

var formatFlag string

var scanCmd = &cobra.Command{
	Use:   "scan [path]",
	Short: "Scan Go source files for SQL query issues",
	Long:  "Statically analyzes Go source files to find SQL queries and check them for common issues.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runScan,
}

func init() {
	scanCmd.Flags().StringVar(&formatFlag, "format", "console", "Output format: console or json")
}

// errIssuesFound is returned when the scan finds issues, to signal a non-zero exit code.
var errIssuesFound = errors.New("issues found")

func runScan(cmd *cobra.Command, args []string) error {
	// Args are valid past this point; don't dump usage for runtime errors or
	// the errIssuesFound sentinel. (Arg-parse errors still show usage.)
	cmd.SilenceUsage = true

	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	rep, err := newReporter(formatFlag)
	if err != nil {
		return err
	}

	cfg, err := resolveConfig(dir)
	if err != nil {
		return err
	}
	a, err := cfg.Analyzer()
	if err != nil {
		return err
	}
	printConfigWarnings(cfg)
	exclude, err := cfg.ExcludeMatcher()
	if err != nil {
		return err
	}

	allResults, totalFiles, err := scanDir(dir, a, exclude)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if len(allResults) > 0 {
		rep.Report(allResults)
		if formatFlag != "json" {
			_, _ = fmt.Fprintf(os.Stderr, "\n%d issue(s) found (%d file(s) scanned)\n", len(allResults), totalFiles)
		}
		return errIssuesFound
	}

	if formatFlag != "json" {
		_, _ = fmt.Fprintf(os.Stderr, "No issues found (%d file(s) scanned)\n", totalFiles)
	}
	return nil
}

func newReporter(format string) (reporter.Reporter, error) {
	switch format {
	case "json":
		return reporter.NewJSONReporter(), nil
	case "console", "":
		return reporter.NewConsoleReporter(), nil
	default:
		return nil, fmt.Errorf("unknown format %q: use 'console' or 'json'", format)
	}
}

// scanDir type-checks the target with golang.org/x/tools/go/packages so query
// arguments that are constants, cross-package constants, constant
// concatenations, or fmt.Sprintf literal format strings all resolve. If the
// target is not a loadable module (no go.mod, ad-hoc files), it degrades to a
// dependency-free go/parser walk that still handles inline string literals, so
// a broken or module-less tree is never silently skipped.
func scanDir(dir string, a *analyzer.Analyzer, exclude func(string) bool) ([]analyzer.Result, int, error) {
	absDir, _ := filepath.Abs(dir)

	if results, n, ok := scanViaPackages(absDir, a, exclude); ok {
		return results, n, nil
	}
	results, n, err := scanViaAST(dir, absDir, a, exclude)
	return results, n, err
}

// scanViaPackages is the primary, type-aware path. ok is false when the target
// cannot be loaded as a module at all (caller then falls back to the AST walk);
// individual packages with type errors are still scanned, degrading per-file to
// literal-only resolution.
func scanViaPackages(absDir string, a *analyzer.Analyzer, exclude func(string) bool) (results []analyzer.Result, totalFiles int, ok bool) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:   absDir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil || len(pkgs) == 0 {
		return nil, 0, false
	}

	seen := map[string]struct{}{}
	scannedAny := false
	for _, pkg := range pkgs {
		if len(pkg.Syntax) == 0 {
			continue
		}
		scannedAny = true
		// Degraded package (type errors): TypesInfo may be partial or nil;
		// constString falls back to *ast.BasicLit when info lacks the value.
		info := pkg.TypesInfo
		for _, file := range pkg.Syntax {
			path := pkg.Fset.Position(file.Pos()).Filename
			if !keepFile(path, absDir, exclude) {
				continue
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			totalFiles++
			results = append(results, scanASTFile(pkg.Fset, file, info, a)...)
		}
	}
	if !scannedAny {
		return nil, 0, false
	}
	return results, totalFiles, true
}

// scanViaAST is the dependency-free fallback for module-less / unbuildable
// trees: parse each file in isolation and resolve only inline string literals
// (info is nil, so scanASTFile degrades accordingly).
func scanViaAST(dir, absDir string, a *analyzer.Analyzer, exclude func(string) bool) ([]analyzer.Result, int, error) {
	fset := token.NewFileSet()
	var results []analyzer.Result
	totalFiles := 0

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return shouldSkipDir(path, absDir)
		}
		if !keepFile(path, absDir, exclude) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return nil
		}
		totalFiles++
		results = append(results, scanASTFile(fset, f, nil, a)...)
		return nil
	})

	return results, totalFiles, err
}

// keepFile reports whether a .go file should be analyzed: skip non-Go and
// _test.go files, then apply the configured exclude matcher against the path
// relative to the scan root (so regexes behave identically whether the path
// came from go list (absolute) or the walk (relative)).
func keepFile(path, absDir string, exclude func(string) bool) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	if exclude != nil {
		rel := path
		if abs, err := filepath.Abs(path); err == nil {
			if r, rerr := filepath.Rel(absDir, abs); rerr == nil {
				rel = r
			}
		}
		if exclude(filepath.ToSlash(rel)) {
			return false
		}
	}
	return true
}

func shouldSkipDir(path, absDir string) error {
	absPath, _ := filepath.Abs(path)
	if absPath != absDir {
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
			return filepath.SkipDir
		}
	}
	return nil
}

// scanASTFile walks one parsed file for SQL-method calls and resolves each
// query argument via resolveQuery. info may be nil (fallback / degraded
// package), in which case resolution is limited to inline string literals.
func scanASTFile(fset *token.FileSet, f *ast.File, info *types.Info, a *analyzer.Analyzer) []analyzer.Result {
	suppress := buildSuppressor(fset, f)

	var results []analyzer.Result
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !sqlMethods[sel.Sel.Name] {
			return true
		}

		arg := queryArgExpr(sel.Sel.Name, call.Args)
		if arg == nil {
			return true
		}
		query := resolveQuery(info, arg)
		if query == "" {
			return true
		}

		found := a.Analyze(query)
		pos := fset.Position(call.Pos())
		all, rules := suppress(pos.Line)
		for _, r := range found {
			if all || rules[r.RuleName] {
				continue
			}
			r.File = pos.Filename
			r.Line = pos.Line
			results = append(results, r)
		}
		return true
	})

	return results
}

// buildSuppressor returns a lookup that, for a given source line, reports
// whether a `// sqlguard:ignore` directive applies — either trailing on that
// line or on the line directly above the call. This is the static-analysis
// counterpart to the in-SQL directive the analyzer handles at runtime.
func buildSuppressor(fset *token.FileSet, f *ast.File) func(line int) (bool, map[string]bool) {
	type directive struct {
		all   bool
		rules map[string]bool
	}
	byLine := map[int]directive{}
	for _, cg := range f.Comments {
		all, rules, found := analyzer.ParseIgnoreComment(cg.Text())
		if !found {
			continue
		}
		end := fset.Position(cg.End()).Line
		// Apply to the comment's own line (trailing) and the next line
		// (comment sitting directly above the call).
		byLine[end] = directive{all, rules}
		byLine[end+1] = directive{all, rules}
	}
	return func(line int) (bool, map[string]bool) {
		d, ok := byLine[line]
		if !ok {
			return false, nil
		}
		return d.all, d.rules
	}
}

// queryArgExpr returns the expression holding the SQL string for a given SQL
// method (the first arg, or the second for *Context variants).
func queryArgExpr(methodName string, args []ast.Expr) ast.Expr {
	argIdx := 0
	if strings.HasSuffix(methodName, "Context") {
		argIdx = 1
	}
	if argIdx >= len(args) {
		return nil
	}
	return args[argIdx]
}

// resolveQuery turns a query-argument expression into SQL text. The single
// go/constant lookup in constString already covers inline literals,
// same-package constants, cross-package constants, and constant concatenation
// (the type checker folded them). fmt.Sprintf with a constant format string is
// resolved by neutralizing its verbs so the SQL stays structurally analyzable.
func resolveQuery(info *types.Info, e ast.Expr) string {
	if s, ok := constString(info, e); ok {
		return s
	}
	if ce, ok := e.(*ast.CallExpr); ok {
		if fa, ok := sprintfFormatArg(info, ce); ok {
			if f, ok := constString(info, fa); ok {
				return neutralizeFormat(f)
			}
		}
	}
	return ""
}

// constString resolves any constant string-valued expression. With type info
// this is one map lookup that the compiler already folded; without it (nil
// info or value absent) it degrades to a raw string literal.
func constString(info *types.Info, e ast.Expr) (string, bool) {
	if info != nil {
		if tv, ok := info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
	}
	if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
		if s, err := strconv.Unquote(bl.Value); err == nil {
			return s, true
		}
	}
	return "", false
}

// sprintfFormatArg returns the format-string argument if ce is a call to
// fmt.Sprintf. With type info the callee is verified to be package "fmt";
// without it, a conservative `fmt.Sprintf` selector-name heuristic is used.
func sprintfFormatArg(info *types.Info, ce *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" || len(ce.Args) == 0 {
		return nil, false
	}
	if info != nil {
		if obj := info.Uses[sel.Sel]; obj != nil {
			fn, ok := obj.(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "fmt" {
				return nil, false
			}
			return ce.Args[0], true
		}
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fmt" {
		return ce.Args[0], true
	}
	return nil, false
}

var formatVerb = regexp.MustCompile(`%[-+# 0]*[\d.*]*[a-zA-Z%]`)

// neutralizeFormat replaces fmt verbs in a constant format string with benign
// placeholders so the remaining SQL keeps its structure for the rule engine.
// Numeric verbs become 0; everything else becomes a harmless identifier; %%
// collapses to a literal %.
func neutralizeFormat(format string) string {
	return formatVerb.ReplaceAllStringFunc(format, func(v string) string {
		switch v[len(v)-1] {
		case '%':
			return "%"
		case 'b', 'c', 'd', 'o', 'O', 'x', 'X', 'U', 'e', 'E', 'f', 'F', 'g', 'G', 'p':
			return "0"
		default:
			return "sqlguard"
		}
	})
}
