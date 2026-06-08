package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createModule turns dir into a loadable Go module so the type-aware
// (go/packages) scan path runs instead of the AST fallback.
func createModule(t *testing.T, dir, modPath string) {
	t.Helper()
	createTestFile(t, dir, "go.mod", "module "+modPath+"\n\ngo 1.26\n")
}

func createFileInSubdir(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan_ResolvesSamePackageConst(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createTestFile(t, dir, "q.go", `package example
import "database/sql"

const userQuery = "SELECT * FROM users WHERE id = 1"

func f(db *sql.DB) {
	db.Query(userQuery)
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected issues from a resolved const, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star from resolved const, got:\n%s", out)
	}
}

func TestScan_ResolvesConstConcatenation(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createTestFile(t, dir, "q.go", `package example
import "database/sql"

const (
	cols = "*"
	q    = "SELECT " + cols + " FROM users WHERE id = 1"
)

func f(db *sql.DB) {
	db.Query(q)
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected issues from folded concatenation, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star from concatenated const, got:\n%s", out)
	}
}

func TestScan_ResolvesCrossPackageConst(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createFileInSubdir(t, dir, "queries/queries.go", `package queries

const GetUser = "SELECT * FROM users WHERE id = 1"
`)
	createTestFile(t, dir, "main.go", `package example
import (
	"database/sql"

	"example.com/m/queries"
)

func f(db *sql.DB) {
	db.Query(queries.GetUser)
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected issues from cross-package const, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star from cross-package const, got:\n%s", out)
	}
}

func TestScan_ResolvesSprintfFormat(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createTestFile(t, dir, "q.go", `package example
import (
	"database/sql"
	"fmt"
)

func f(db *sql.DB, table string) {
	db.Query(fmt.Sprintf("SELECT * FROM %s WHERE id = %d", table, 1))
}
`)

	out, err := captureScanOutput(t, dir)

	if !errors.Is(err, errIssuesFound) {
		t.Fatalf("expected issues from Sprintf format string, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "select-star") {
		t.Errorf("expected select-star from Sprintf format, got:\n%s", out)
	}
}

// A safe query held in a constant must stay clean — proves resolution does not
// introduce false positives.
func TestScan_ResolvedConstNoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createTestFile(t, dir, "q.go", `package example
import "database/sql"

const safe = "SELECT id, name FROM users WHERE id = ? LIMIT 10"

func f(db *sql.DB) {
	db.Query(safe, 1)
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Fatalf("expected clean exit for safe resolved const, got %v\n%s", err, out)
	}
	if strings.Contains(out, "SQLGUARD") {
		t.Errorf("expected no findings for safe const, got:\n%s", out)
	}
}

// Inline suppression must still apply when the query is a resolved const.
func TestScan_SuppressionWithResolvedConst(t *testing.T) {
	dir := t.TempDir()
	createModule(t, dir, "example.com/m")
	createTestFile(t, dir, "q.go", `package example
import "database/sql"

const userQuery = "SELECT * FROM users WHERE id = 1"

func f(db *sql.DB) {
	db.Query(userQuery) // sqlguard:ignore:select-star
}
`)

	out, err := captureScanOutput(t, dir)

	if err != nil {
		t.Fatalf("expected clean exit, finding suppressed, got %v\n%s", err, out)
	}
	if strings.Contains(out, "select-star") {
		t.Errorf("inline directive should suppress finding on resolved const, got:\n%s", out)
	}
}
