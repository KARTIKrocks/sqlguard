package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// Reset format flag to default for each test
	formatFlag = "console"

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := runScan(&cobra.Command{}, []string{dir})

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
