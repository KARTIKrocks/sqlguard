package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/reporter"
	"github.com/spf13/cobra"
)

func createTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
}

func TestScan_DetectsSelectStar(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users WHERE id = 1")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit (errIssuesFound)")
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star warning, got:\n%s", out)
	}
}

func TestScan_DetectsDeleteWithoutWhere(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Exec("DELETE FROM users")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "delete-without-where") {
		t.Errorf("expected delete-without-where warning, got:\n%s", out)
	}
	if !strings.Contains(out, "CRITICAL") {
		t.Errorf("expected CRITICAL severity, got:\n%s", out)
	}
}

func TestScan_DetectsLeadingWildcard(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id FROM users WHERE name LIKE '%test%'")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "leading-wildcard") {
		t.Errorf("expected leading-wildcard warning, got:\n%s", out)
	}
}

func TestScan_DetectsUpdateWithoutWhere(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Exec("UPDATE users SET name = 'test'")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "update-without-where") {
		t.Errorf("expected update-without-where warning, got:\n%s", out)
	}
}

func TestScan_DetectsInsertWithoutColumns(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Exec("INSERT INTO users VALUES ('alice', 'alice@test.com')")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "insert-without-columns") {
		t.Errorf("expected insert-without-columns warning, got:\n%s", out)
	}
}

func TestScan_DetectsSelectWithoutLimit(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id, name FROM users")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "select-without-limit") {
		t.Errorf("expected select-without-limit warning, got:\n%s", out)
	}
}

func TestScan_DetectsOrderByWithoutLimit(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id FROM users WHERE active = true ORDER BY name")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "orderby-without-limit") {
		t.Errorf("expected orderby-without-limit warning, got:\n%s", out)
	}
}

func TestScan_NoWarningsForSafeQuery(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "good.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id, name FROM users WHERE id = ? LIMIT 10", 1)
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Errorf("expected nil error for safe query, got: %v", err)
	}
	if strings.Contains(out, "SQLGUARD") {
		t.Errorf("expected no warnings for safe query, got:\n%s", out)
	}
	if !strings.Contains(out, "No issues found (") {
		t.Errorf("expected 'No issues found' message, got:\n%s", out)
	}
}

func TestScan_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad_test.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users WHERE id = 1")
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if strings.Contains(out, "select-star") {
		t.Errorf("should skip _test.go files, got:\n%s", out)
	}
}

func TestScan_SkipsVendorDir(t *testing.T) {
	vendorDir := filepath.Join(t.TempDir(), "vendor")
	os.MkdirAll(vendorDir, 0755)
	createTestFile(t, vendorDir, "bad.go", `package vendor
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users WHERE id = 1")
}
`)

	out, err := captureScanOutput(t, filepath.Dir(vendorDir))

	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if strings.Contains(out, "select-star") {
		t.Errorf("should skip vendor directory, got:\n%s", out)
	}
}

func TestScan_HandlesContextMethods(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "ctx.go", `package example
import (
	"context"
	"database/sql"
)
func f(db *sql.DB) {
	db.QueryContext(context.Background(), "SELECT * FROM users WHERE id = 1")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star for QueryContext, got:\n%s", out)
	}
}

func TestScan_MultipleIssuesInOneFile(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "multi.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users WHERE email LIKE '%test%'")
	db.Exec("DELETE FROM orders")
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(out, "select-star") {
		t.Error("expected select-star warning")
	}
	if !strings.Contains(out, "leading-wildcard") {
		t.Error("expected leading-wildcard warning")
	}
	if !strings.Contains(out, "delete-without-where") {
		t.Error("expected delete-without-where warning")
	}
}

// boundDir creates a temp dir with a .git marker so config.Discover does not
// escape it while walking parents.
func boundDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScan_ConfigDisablesRule(t *testing.T) {
	dir := boundDir(t)
	createTestFile(t, dir, ".sqlguard.yml", "rules:\n  disable: [select-star]\n")
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users WHERE id = 1")
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Errorf("expected clean exit when rule disabled by config, got %v\n%s", err, out)
	}
	if strings.Contains(out, "select-star") {
		t.Errorf("select-star should be disabled via .sqlguard.yml, got:\n%s", out)
	}
}

func TestScan_InlineSuppressionComment(t *testing.T) {
	dir := boundDir(t)
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	// sqlguard:ignore
	db.Exec("DELETE FROM users")
	db.Query("SELECT * FROM users WHERE id = 1") // sqlguard:ignore:select-star
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Errorf("expected clean exit, all findings suppressed, got %v\n%s", err, out)
	}
	if strings.Contains(out, "delete-without-where") || strings.Contains(out, "select-star") {
		t.Errorf("inline directives should suppress findings, got:\n%s", out)
	}
}

func TestScan_ExitCodeZeroWhenClean(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "clean.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id, name FROM users WHERE id = ? LIMIT 10", 1)
}
`)

	_, err := captureScanOutput(t, dir)

	if err != nil {
		t.Errorf("expected exit code 0 for clean code, got error: %v", err)
	}
}

// captureScanOutput runs the scan command and captures stderr output.
// Returns the output and the error (errIssuesFound if issues were found).
func captureScanOutput(t *testing.T, dir string) (string, error) {
	t.Helper()
	return captureScanTarget(t, dir)
}

// captureScanTarget is captureScanOutput for a target that is not a plain
// directory path, such as the `./...` package-pattern spelling.
func captureScanTarget(t *testing.T, target string) (string, error) {
	t.Helper()

	// Reset format flag to default for each test
	formatFlag = "console"

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := runScan(&cobra.Command{}, []string{target})

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Only fail on unexpected errors, not errIssuesFound
	if err != nil && !errors.Is(err, errIssuesFound) {
		t.Fatalf("scan failed unexpectedly: %v", err)
	}

	return buf.String(), err
}

// TestScanCommand_NoUsageDumpOnIssues runs the real command tree (rootCmd.Execute)
// and asserts that a normal "issues found" outcome does NOT print cobra's usage
// text. Regression guard for the SilenceErrors/SilenceUsage wiring: without it,
// returning errIssuesFound from RunE makes cobra dump "Error: issues found"
// followed by the full usage, which looks like a CLI misuse.
func TestScanCommand_NoUsageDumpOnIssues(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go",
		"package bad\n\nimport \"database/sql\"\n\nfunc r(d *sql.DB) { d.Exec(\"DELETE FROM x\") }\n")

	formatFlag = "console"
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	rootCmd.SetArgs([]string{"scan", "--no-config", dir})
	err := rootCmd.Execute()

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected errIssuesFound, got %v", err)
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("scan dumped usage text on an issues-found result:\n%s", out)
	}
	if !strings.Contains(out, "delete-without-where") {
		t.Errorf("expected the finding in output, got:\n%s", out)
	}
}

func TestTrimPatternSuffix(t *testing.T) {
	// sep is the OS separator the call is evaluated under, so Windows
	// behavior is covered from a Unix host and vice versa.
	cases := []struct {
		sep      rune
		in, want string
	}{
		// Slash patterns work on every platform.
		{'/', "./...", "."},
		{'/', "...", "."},
		{'/', "/...", "/"},
		{'/', "./pkg/...", "./pkg"},
		{'/', "pkg/...", "pkg"},
		{'/', "/abs/pkg/...", "/abs/pkg"},
		{'\\', "./...", "."},
		{'\\', "...", "."},
		{'\\', "./pkg/...", "./pkg"},

		// Plain paths are untouched.
		{'/', ".", "."},
		{'/', "./pkg", "./pkg"},
		{'/', "/abs/pkg", "/abs/pkg"},
		{'/', "", ""},

		// Separator-anchored: directory names, not patterns.
		{'/', "weird...", "weird..."},
		{'/', "./weird...", "./weird..."},
		{'/', "....", "...."},
		{'/', "/abs/weird...", "/abs/weird..."},

		// A backslash is an ordinary filename byte on Unix, so `weird\...`
		// and `.\...` are directory names there — but patterns on Windows.
		{'/', `.\...`, `.\...`},
		{'/', `weird\...`, `weird\...`},
		{'/', `./a\...`, `./a\...`},
		{'\\', `.\...`, "."},
		{'\\', `.\pkg\...`, `.\pkg`},
		{'\\', `pkg\...`, "pkg"},
		{'\\', `weird...`, `weird...`},

		// Bare roots keep their separator.
		{'\\', `C:\...`, `C:\`},
		{'\\', `\...`, `\`},
	}
	for _, c := range cases {
		if got := trimPatternSuffixSep(c.in, c.sep); got != c.want {
			t.Errorf("trimPatternSuffixSep(%q, %q) = %q, want %q", c.in, c.sep, got, c.want)
		}
	}
}

// TestScan_AcceptsPackagePattern pins the `./...` spelling the README and the
// docs site use. It used to reach filepath.Abs verbatim and fail with
// "lstat ./...: no such file or directory", so the documented invocation was
// the one invocation that did not work. The nested file also proves the
// pattern still reaches subdirectories rather than silently scanning one level.
func TestScan_AcceptsPackagePattern(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "top.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users")
}
`)
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	createTestFile(t, sub, "deep.go", `package nested
import "database/sql"
func g(db *sql.DB) {
	db.Exec("DELETE FROM sessions")
}
`)

	out, err := captureScanTarget(t, filepath.Join(dir, "..."))

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected errIssuesFound for ./... target, got %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("pattern target missed the top-level file, got:\n%s", out)
	}
	if !strings.Contains(out, "delete-without-where") {
		t.Errorf("pattern target did not recurse into the subdirectory, got:\n%s", out)
	}
}

// TestScan_PatternMatchesPlainPath is the equivalence the fix rests on: the
// scan is already recursive, so `dir/...` must select exactly what `dir` does.
func TestScan_PatternMatchesPlainPath(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "a.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users")
}
`)

	plain, errPlain := captureScanTarget(t, dir)
	pattern, errPattern := captureScanTarget(t, filepath.Join(dir, "..."))

	if !errors.Is(errPlain, errIssuesFound) || !errors.Is(errPattern, errIssuesFound) {
		t.Fatalf("both spellings should report issues: plain=%v pattern=%v", errPlain, errPattern)
	}
	if plain != pattern {
		t.Errorf("plain and pattern targets disagree:\nplain:\n%s\npattern:\n%s", plain, pattern)
	}
}

// TestScan_DottedDirectoryIsNotAPattern guards the separator anchor. A
// directory named `weird...` is a legal path; trimming the suffix unanchored
// pointed the scan at a sibling `weird` instead, which reported that
// directory's findings under the name the user did not ask for — and would
// have exited clean had the sibling been clean.
func TestScan_DottedDirectoryIsNotAPattern(t *testing.T) {
	root := t.TempDir()
	dotted := filepath.Join(root, "weird...")
	plain := filepath.Join(root, "weird")
	for _, d := range []string{dotted, plain} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	createTestFile(t, dotted, "a.go", `package weird
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users")
}
`)
	createTestFile(t, plain, "b.go", `package weird
import "database/sql"
func g(db *sql.DB) {
	db.Exec("DELETE FROM sessions")
}
`)

	out, err := captureScanTarget(t, dotted)

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected the dotted directory to be scanned, got %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("did not scan the directory that was named, got:\n%s", out)
	}
	if strings.Contains(out, "delete-without-where") {
		t.Errorf("scanned the sibling directory instead of the one named, got:\n%s", out)
	}
}

// captureScanStreams runs a scan capturing stdout and stderr separately, which
// is what the stream split has to be asserted on.
func captureScanStreams(t *testing.T, target, format string) (stdout, stderr string, err error) {
	t.Helper()

	formatFlag = format
	t.Cleanup(func() { formatFlag = "console" })

	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	// Drain both pipes while the scan runs. Reading only after it returns
	// deadlocks as soon as either stream exceeds the pipe buffer (64 KiB on
	// Linux) — the scan blocks writing, the test blocks waiting for it, and
	// the package hangs until the go test timeout.
	var bufOut, bufErr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = bufOut.ReadFrom(rOut) }()
	go func() { defer wg.Done(); _, _ = bufErr.ReadFrom(rErr) }()

	err = runScan(&cobra.Command{}, []string{target})

	wOut.Close()
	wErr.Close()
	wg.Wait()
	rOut.Close()
	rErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	if err != nil && !errors.Is(err, errIssuesFound) {
		t.Fatalf("scan failed unexpectedly: %v", err)
	}
	return bufOut.String(), bufErr.String(), err
}

// TestScan_JSONGoesToStdout pins the stream split. JSON was written to stderr,
// so `sqlguard scan --format json > out.json` — the only reason a
// machine-readable format exists — produced an empty file.
func TestScan_JSONGoesToStdout(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users")
}
`)
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	stdout, stderr, err := captureScanStreams(t, dir, "json")

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected errIssuesFound, got %v", err)
	}
	if strings.Contains(stderr, "select-star") {
		t.Errorf("JSON findings leaked onto stderr:\n%s", stderr)
	}
	var got []map[string]any
	if uerr := json.Unmarshal([]byte(stdout), &got); uerr != nil {
		t.Fatalf("stdout is not valid JSON (%v):\n%s", uerr, stdout)
	}
	if len(got) == 0 {
		t.Fatalf("expected findings in the JSON array, got %s", stdout)
	}
	if got[0]["rule"] != "select-star" {
		t.Errorf("unexpected first rule %v in:\n%s", got[0]["rule"], stdout)
	}
}

// TestScan_JSONEmptyArrayWhenClean guards the other half of the redirect story:
// a clean run used to emit nothing at all, handing a consumer an empty file
// rather than a parseable empty array.
func TestScan_JSONEmptyArrayWhenClean(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "clean.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id, name FROM users WHERE id = ? LIMIT 10", 1)
}
`)
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	stdout, _, err := captureScanStreams(t, dir, "json")

	if err != nil {
		t.Fatalf("expected exit 0 on a clean tree, got %v", err)
	}
	// The promise is literally `[]`. Decoding alone would not pin it: `null`
	// unmarshals into a slice without error and leaves it nil with length 0,
	// so a reporter that switched to a nil slice would emit `null` and still
	// satisfy a len()-only assertion.
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("clean run must emit []; got %q", stdout)
	}
	var got []map[string]any
	if uerr := json.Unmarshal([]byte(stdout), &got); uerr != nil {
		t.Fatalf("clean run must still emit a parseable array, got %q (%v)", stdout, uerr)
	}
	if got == nil {
		t.Errorf("decoded to a nil slice, which means `null` rather than `[]`: %q", stdout)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty array, got %s", stdout)
	}
}

// TestScan_ConsoleStaysOnStderr is the other side of the split: the console
// rendering shares stderr with the progress and summary lines, so a CI step
// redirecting stdout does not swallow the human-readable report.
func TestScan_ConsoleStaysOnStderr(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "bad.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Exec("DELETE FROM sessions")
}
`)
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	stdout, stderr, err := captureScanStreams(t, dir, "console")

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected errIssuesFound, got %v", err)
	}
	if stdout != "" {
		t.Errorf("console format wrote to stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "delete-without-where") {
		t.Errorf("console findings missing from stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "issue(s) found") {
		t.Errorf("summary line missing from stderr:\n%s", stderr)
	}
}

// TestScan_JSONLargeOutputDoesNotDeadlock exercises more output than a pipe
// buffer holds (64 KiB on Linux). With the capture helper reading only after
// runScan returned, the scan blocked mid-write and the package hung until the
// go test timeout rather than failing.
func TestScan_JSONLargeOutputDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package example\n\nimport \"database/sql\"\n\nfunc f(db *sql.DB) {\n")
	for i := range 400 {
		fmt.Fprintf(&src, "\tdb.Query(\"SELECT * FROM table_%d\")\n", i)
	}
	src.WriteString("}\n")
	createTestFile(t, dir, "many.go", src.String())

	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	stdout, _, err := captureScanStreams(t, dir, "json")

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected errIssuesFound, got %v", err)
	}
	if len(stdout) <= 64*1024 {
		t.Fatalf("fixture no longer exceeds the pipe buffer (%d bytes); "+
			"the deadlock this test guards would not reproduce", len(stdout))
	}
	var got []map[string]any
	if uerr := json.Unmarshal([]byte(stdout), &got); uerr != nil {
		t.Fatalf("large output is not valid JSON: %v", uerr)
	}
	if len(got) < 400 {
		t.Errorf("expected at least 400 findings, got %d", len(got))
	}
}

// TestCheckedWriter_RecordsFirstError pins the write-failure path. stdout is
// the product now, so a partial write must fail the run rather than leave a
// truncated document behind a zero exit.
func TestCheckedWriter_RecordsFirstError(t *testing.T) {
	sentinel := errors.New("disk full")
	cw := &checkedWriter{w: &failingWriter{after: 4, err: sentinel}}

	_, _ = cw.Write([]byte("ok"))
	if cw.err != nil {
		t.Fatalf("no error expected before the limit, got %v", cw.err)
	}
	_, _ = cw.Write([]byte("more"))
	if !errors.Is(cw.err, sentinel) {
		t.Fatalf("expected the write error to be recorded, got %v", cw.err)
	}

	second := errors.New("second")
	_, _ = cw.Write([]byte("x"))
	cw.w = &failingWriter{after: 0, err: second}
	_, _ = cw.Write([]byte("y"))
	if !errors.Is(cw.err, sentinel) {
		t.Errorf("the first error should be kept, got %v", cw.err)
	}

	if err := cw.writeErr(); !errors.Is(err, sentinel) {
		t.Errorf("writeErr should surface the recorded error, got %v", err)
	}

	if err := (&checkedWriter{w: io.Discard}).writeErr(); err != nil {
		t.Errorf("a writer that never failed reports no error, got %v", err)
	}
}

// failingWriter accepts `after` bytes, then fails every write. The pointer
// receiver matters: the byte count has to accumulate across calls.
type failingWriter struct {
	written int
	after   int
	err     error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.after {
		return 0, f.err
	}
	f.written += len(p)
	return len(p), nil
}

// recordingReporter captures what it was handed, including whether it was
// called at all — which is the difference between the JSON and console paths.
type recordingReporter struct {
	calls   int
	results []analyzer.Result
}

func (r *recordingReporter) Report(results []analyzer.Result) {
	r.calls++
	r.results = append(r.results, results...)
}

// The output policy below is shared by scan and explain. explain's own path
// cannot be reached in a unit test without a live database, so the logic lives
// in one helper and is pinned here instead: the JSON and console branches, the
// exit codes, and the write-error escalation.

func TestReport_JSONPolicy(t *testing.T) {
	noWriteErr := func() error { return nil }

	t.Run("reports even when clean", func(t *testing.T) {
		rep := &recordingReporter{}
		var foundCalled, cleanCalled bool
		err := report(rep, "json", nil, noWriteErr,
			func() { foundCalled = true }, func() { cleanCalled = true })

		if err != nil {
			t.Errorf("clean json run should exit 0, got %v", err)
		}
		if rep.calls != 1 {
			t.Errorf("json must report unconditionally so a redirect gets an array; calls=%d", rep.calls)
		}
		if foundCalled || cleanCalled {
			t.Errorf("summary lines must stay off the json path (found=%v clean=%v)", foundCalled, cleanCalled)
		}
	})

	t.Run("findings exit non-zero", func(t *testing.T) {
		rep := &recordingReporter{}
		err := report(rep, "json", []analyzer.Result{{RuleName: "select-star"}}, noWriteErr,
			func() {}, func() {})

		if !errors.Is(err, errIssuesFound) {
			t.Errorf("expected errIssuesFound, got %v", err)
		}
		if rep.calls != 1 || len(rep.results) != 1 {
			t.Errorf("expected the finding reported once, calls=%d results=%d", rep.calls, len(rep.results))
		}
	})
}

func TestReport_ConsolePolicy(t *testing.T) {
	noWriteErr := func() error { return nil }

	t.Run("stays quiet when clean", func(t *testing.T) {
		rep := &recordingReporter{}
		var cleanCalled bool
		err := report(rep, "console", nil, noWriteErr, func() {}, func() { cleanCalled = true })

		if err != nil {
			t.Errorf("clean console run should exit 0, got %v", err)
		}
		if rep.calls != 0 {
			t.Errorf("console must not render an empty report, calls=%d", rep.calls)
		}
		if !cleanCalled {
			t.Error("the clean summary line should have been written")
		}
	})

	t.Run("findings report and summarise", func(t *testing.T) {
		rep := &recordingReporter{}
		var foundCalled bool
		err := report(rep, "console", []analyzer.Result{{RuleName: "select-star"}}, noWriteErr,
			func() { foundCalled = true }, func() {})

		if !errors.Is(err, errIssuesFound) {
			t.Errorf("expected errIssuesFound, got %v", err)
		}
		if rep.calls != 1 || !foundCalled {
			t.Errorf("expected one report plus the summary line, calls=%d found=%v", rep.calls, foundCalled)
		}
	})
}

// TestReport_WriteErrorWins pins the escalation: a truncated artifact must not
// be reported as a findings-only result, nor as a clean run.
func TestReport_WriteErrorWins(t *testing.T) {
	sentinel := errors.New("no space left on device")
	failing := func() error { return sentinel }

	for _, tc := range []struct {
		name    string
		results []analyzer.Result
	}{
		{"with findings", []analyzer.Result{{RuleName: "select-star"}}},
		{"otherwise clean", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := report(&recordingReporter{}, "json", tc.results, failing, func() {}, func() {})
			if !errors.Is(err, sentinel) {
				t.Errorf("expected the write error, got %v", err)
			}
			if errors.Is(err, errIssuesFound) {
				t.Error("the write failure should be the reported error")
			}
		})
	}
}

// TestNewReporter_JSONTargetsStdout pins the routing itself, independently of
// any command, so explain inherits the guarantee from the same helper it uses.
func TestNewReporter_JSONTargetsStdout(t *testing.T) {
	// The reporter fixes its writer at construction (see reporter.JSONReporter),
	// so the swap has to happen before newReporter, not after.
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	rep, writeErr, err := newReporter("json")
	if err != nil {
		os.Stdout, os.Stderr = oldOut, oldErr
		t.Fatalf("json format should be accepted: %v", err)
	}
	if writeErr() != nil {
		os.Stdout, os.Stderr = oldOut, oldErr
		t.Fatalf("a fresh reporter has no write error: %v", writeErr())
	}
	jr, ok := rep.(*reporter.JSONReporter)
	if !ok {
		os.Stdout, os.Stderr = oldOut, oldErr
		t.Fatalf("expected a *reporter.JSONReporter, got %T", rep)
	}

	var bufOut, bufErr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = bufOut.ReadFrom(rOut) }()
	go func() { defer wg.Done(); _, _ = bufErr.ReadFrom(rErr) }()

	jr.Report([]analyzer.Result{{RuleName: "select-star"}})

	wOut.Close()
	wErr.Close()
	wg.Wait()
	rOut.Close()
	rErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	if !strings.Contains(bufOut.String(), "select-star") {
		t.Errorf("json reporter did not write to stdout:\n%s", bufOut.String())
	}
	if bufErr.Len() != 0 {
		t.Errorf("json reporter wrote to stderr:\n%s", bufErr.String())
	}
}

// TestNewReporter_EachCallGetsItsOwnWriteError guards against the write result
// living in package state, where a second invocation in the same process could
// clear or claim the first one's failure. The first reporter is built over a
// stdout that is already closed, so its write genuinely fails; the second is
// built over a working pipe and must stay clean.
func TestNewReporter_EachCallGetsItsOwnWriteError(t *testing.T) {
	oldOut := os.Stdout
	defer func() { os.Stdout = oldOut }()

	rBad, wBad, _ := os.Pipe()
	rBad.Close()
	wBad.Close()
	os.Stdout = wBad
	repBroken, brokenErr, err := newReporter("json")
	if err != nil {
		t.Fatalf("newReporter (broken stdout): %v", err)
	}

	rOK, wOK, _ := os.Pipe()
	os.Stdout = wOK
	repOK, okErr, err := newReporter("json")
	if err != nil {
		t.Fatalf("newReporter (working stdout): %v", err)
	}

	var sink bytes.Buffer
	var wg sync.WaitGroup
	wg.Go(func() { ; _, _ = sink.ReadFrom(rOK) })

	repBroken.Report([]analyzer.Result{{RuleName: "select-star"}})
	repOK.Report([]analyzer.Result{{RuleName: "select-star"}})

	wOK.Close()
	wg.Wait()
	rOK.Close()
	os.Stdout = oldOut

	if brokenErr() == nil {
		t.Error("the failed write was not recorded by its own reporter")
	}
	if okErr() != nil {
		t.Errorf("the healthy reporter inherited another run's failure: %v", okErr())
	}
	if !strings.Contains(sink.String(), "select-star") {
		t.Errorf("the healthy reporter did not write its report:\n%s", sink.String())
	}
}

// TestScan_DotsDirectoryWarns covers the one spelling where the pattern
// reading hides a real directory. `...` stays a pattern — that is what every
// Go tool does, and the go command cannot address such a directory at all —
// but the run must say so, or a clean exit looks like the named tree was
// examined when it was never opened.
func TestScan_DotsDirectoryWarns(t *testing.T) {
	root := t.TempDir()
	queries := filepath.Join(root, "queries")
	dots := filepath.Join(queries, "...")
	if err := os.MkdirAll(dots, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	createTestFile(t, dots, "hidden.go", `package dots
import "database/sql"
func f(db *sql.DB) {
	db.Exec("DELETE FROM audit_log")
}
`)
	createTestFile(t, queries, "sibling.go", `package queries
import "database/sql"
func g(db *sql.DB) {
	db.Query("SELECT id FROM t WHERE id = ? LIMIT 1", 1)
}
`)
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	// The pattern reading wins, and says so.
	_, stderr, err := captureScanStreams(t, filepath.Join(queries, "..."), "console")
	if err != nil {
		t.Fatalf("expected a clean scan of the parent, got %v", err)
	}
	if !strings.Contains(stderr, "both a package pattern and an existing directory") {
		t.Errorf("ambiguity was not reported:\n%s", stderr)
	}
	if strings.Contains(stderr, "delete-without-where") {
		t.Errorf("pattern reading should not have entered the dots directory:\n%s", stderr)
	}

	// The trailing separator reaches the directory itself.
	_, stderr, err = captureScanStreams(t, dots+string(filepath.Separator), "console")
	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("trailing-separator form should scan the directory, got %v", err)
	}
	if !strings.Contains(stderr, "delete-without-where") {
		t.Errorf("trailing-separator form missed the finding:\n%s", stderr)
	}
	if strings.Contains(stderr, "both a package pattern") {
		t.Errorf("unambiguous form should not warn:\n%s", stderr)
	}
}

// TestScan_NoWarningWithoutDotsDirectory keeps the warning off the ordinary
// path: `./pkg/...` where no such directory exists must stay silent.
func TestScan_NoWarningWithoutDotsDirectory(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "a.go", `package example
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT id FROM t WHERE id = ? LIMIT 1", 1)
}
`)
	noConfigFlag = true
	t.Cleanup(func() { noConfigFlag = false })

	_, stderr, err := captureScanStreams(t, filepath.Join(dir, "..."), "console")
	if err != nil {
		t.Fatalf("expected a clean scan, got %v", err)
	}
	if strings.Contains(stderr, "both a package pattern") {
		t.Errorf("warned with no dots directory present:\n%s", stderr)
	}
}
