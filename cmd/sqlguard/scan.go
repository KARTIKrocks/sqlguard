package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io"
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
	Long: "Statically analyzes Go source files to find SQL queries and check them for common issues.\n\n" +
		"The scan is always recursive. A path may be written either plainly (./pkg)\n" +
		"or with the Go package-pattern suffix (./pkg/...); both select the same files.",
	Args: cobra.MaximumNArgs(1),
	RunE: runScan,
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
		dir = trimPatternSuffix(args[0])
	}

	rep, writeErr, err := newReporter(formatFlag)
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

	return report(rep, formatFlag, allResults, writeErr,
		func() {
			_, _ = fmt.Fprintf(os.Stderr, "\n%d issue(s) found (%d file(s) scanned)\n", len(allResults), totalFiles)
		},
		func() {
			_, _ = fmt.Fprintf(os.Stderr, "No issues found (%d file(s) scanned)\n", totalFiles)
		})
}

// trimPatternSuffix accepts the `./...` spelling every Go tool takes. The scan
// is already recursive, so `dir/...` selects exactly what `dir` does and the
// suffix only has to be removed before the path reaches the filesystem.
func trimPatternSuffix(path string) string {
	return trimPatternSuffixSep(path, filepath.Separator)
}

// trimPatternSuffixSep takes the separator explicitly so both platforms'
// behavior is testable from either one.
//
// The suffix must be separator-anchored: a directory really named `weird...`
// is a legal path, and trimming it unanchored would silently scan `weird`
// instead and report a clean exit for a tree that was never looked at.
//
// `\...` counts only where the OS separator is a backslash. The go command
// rewrites `\` to `/` in relative arguments "as a courtesy to Windows
// developers" (cmd/go/internal/search), so `.\...` is a spelling users do
// type — but on Unix a backslash is an ordinary filename byte, and a directory
// named `weird\...` there must reach the filesystem intact.
func trimPatternSuffixSep(path string, sep rune) string {
	if path == "..." {
		return "."
	}
	trimmed, ok := strings.CutSuffix(path, "/...")
	if !ok && sep == '\\' {
		trimmed, ok = strings.CutSuffix(path, `\...`)
	}
	if !ok {
		return path
	}
	// A bare root ("/...", or "C:\..." on Windows) loses its separator above.
	if trimmed == "" || strings.HasSuffix(trimmed, ":") {
		return trimmed + string(sep)
	}
	return trimmed
}

// checkedWriter remembers the first write error. reporter.Reporter cannot
// return one — Report has no error result — so the CLI records it here and
// reports it as a non-zero exit instead of writing a truncated document and
// claiming success.
type checkedWriter struct {
	w   io.Writer
	err error
}

func (c *checkedWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// writeErr reports a failed write, so a truncated artifact on a full disk
// fails the step instead of passing as green.
func (c *checkedWriter) writeErr() error {
	if c.err != nil {
		return fmt.Errorf("writing JSON output: %w", c.err)
	}
	return nil
}

// newReporter sends machine-readable output to stdout and human-readable
// output to stderr. JSON is the program's product — `--format json > out.json`
// and a pipe both have to receive it — while the console format is a
// diagnostic that shares stderr with the progress and summary lines.
//
// The returned writeErr belongs to this reporter rather than to package state,
// so two invocations in one process cannot read each other's write result.
func newReporter(format string) (rep reporter.Reporter, writeErr func() error, err error) {
	switch format {
	case "json":
		out := &checkedWriter{w: os.Stdout}
		return reporter.NewJSONReporterTo(out), out.writeErr, nil
	case "console", "":
		return reporter.NewConsoleReporter(), func() error { return nil }, nil
	default:
		return nil, nil, fmt.Errorf("unknown format %q: use 'console' or 'json'", format)
	}
}

// report applies the output policy both commands share. JSON always reports,
// so a redirect receives an array even on a clean run; the console format
// stays quiet unless there is something to render, and leaves the wording of
// the summary lines to the caller. Findings mean errIssuesFound either way.
func report(rep reporter.Reporter, format string, results []analyzer.Result, writeErr func() error, found, clean func()) error {
	if format == "json" {
		rep.Report(results)
		if err := writeErr(); err != nil {
			return err
		}
		if len(results) > 0 {
			return errIssuesFound
		}
		return nil
	}

	if len(results) > 0 {
		rep.Report(results)
		found()
		return errIssuesFound
	}

	clean()
	return nil
}

// scanDir type-checks the target with golang.org/x/tools/go/packages so query
// arguments that are constants, cross-package constants, constant
// concatenations, or fmt.Sprintf literal format strings all resolve. If the
// target is not a loadable module (no go.mod, ad-hoc files), it degrades to a
// dependency-free go/parser walk that still handles inline string literals, so
// a broken or module-less tree is never silently skipped.
func scanDir(dir string, a *analyzer.Analyzer, exclude func(string) bool) ([]analyzer.Result, int, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot resolve absolute path: %w", err)
	}

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
			//nolint:nilerr // an unparseable Go file is skipped, not fatal:
			// keep walking the tree rather than aborting the whole scan.
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
