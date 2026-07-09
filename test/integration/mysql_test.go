//go:build integration

package integration

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/KARTIKrocks/sqlguard/explain"
)

// server is one MySQL-dialect database under test. MySQL and MariaDB share the
// "mysql" dialect but disagree on EXPLAIN's column set, so every case runs
// against both.
type server struct {
	name string
	env  string
}

var mysqlServers = []server{
	{"mysql", envMySQL},
	{"mariadb", envMariaDB},
}

// setupMySQL creates a table whose `name` column has no index. 500 rows keeps
// the seeding CTE under MySQL's default cte_max_recursion_depth of 1000 while
// still being large enough for the optimizer to choose a full scan.
func setupMySQL(t *testing.T, env string) *sql.DB {
	t.Helper()
	db := connect(t, env, "mysql")
	exec(t, db,
		`DROP TABLE IF EXISTS users`,
		`CREATE TABLE users (id int AUTO_INCREMENT PRIMARY KEY, email varchar(64), name varchar(64))`,
		`INSERT INTO users (email, name)
		 WITH RECURSIVE seq(n) AS (
		   SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 500
		 )
		 SELECT CONCAT('e', n), CONCAT('n', n) FROM seq`,
		`ANALYZE TABLE users`,
	)
	return db
}

// eachServer runs fn against every configured MySQL-dialect server.
func eachServer(t *testing.T, fn func(t *testing.T, db *sql.DB)) {
	t.Helper()
	for _, s := range mysqlServers {
		t.Run(s.name, func(t *testing.T) {
			fn(t, setupMySQL(t, s.env))
		})
	}
}

// Regression: MySQL 9 defaults @@explain_format to TREE, which returns a
// single free-text column instead of the tabular plan. analyzeMySQL must pin
// FORMAT=TRADITIONAL, or the column lookup finds nothing and every rule goes
// silent. MariaDB emits 10 columns to MySQL's 12, so the lookup must also be
// by name rather than by position.
func TestMySQL_FullTableScanAndMissingIndex(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql")

		res, err := p.Analyze(testCtx(t), `SELECT * FROM users WHERE name = 'n1'`)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if !hasRule(res.Issues, "full-table-scan") {
			t.Errorf("expected full-table-scan, got %v", ruleNames(res.Issues))
		}
		if !hasRule(res.Issues, "no-index-used") {
			t.Errorf("expected no-index-used, got %v", ruleNames(res.Issues))
		}
	})
}

// The negative control: a primary-key lookup uses an index and scans nothing.
func TestMySQL_IndexedLookupIsClean(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql")

		res, err := p.Analyze(testCtx(t), `SELECT * FROM users WHERE id = 1`)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if hasRule(res.Issues, "full-table-scan") || hasRule(res.Issues, "no-index-used") {
			t.Errorf("primary-key lookup should be clean, got %v", ruleNames(res.Issues))
		}
	})
}

// ORDER BY on an unindexed column sorts outside the index.
func TestMySQL_Filesort(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql")

		res, err := p.Analyze(testCtx(t), `SELECT * FROM users ORDER BY name`)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if !hasRule(res.Issues, "filesort") {
			t.Errorf("expected filesort, got %v", ruleNames(res.Issues))
		}
	})
}

// A UNION adds a UNION RESULT row naming the temporary table `<union1,2>`,
// which has type=ALL and no key. It is not a real table, so it must not be
// reported as an unindexed full scan.
func TestMySQL_UnionPseudoTableNotReported(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql")

		res, err := p.Analyze(testCtx(t), `SELECT id FROM users UNION SELECT id FROM users`)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		for _, r := range res.Issues {
			if strings.Contains(r.Message, "<") {
				t.Errorf("issue names a temporary table, not a real one: %q", r.Message)
			}
		}
	})
}

// Without WithAllowDML, a DELETE never reaches the server.
func TestMySQL_DMLRefusedByDefault(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql")

		_, err := p.Analyze(testCtx(t), `DELETE FROM users WHERE id = 1`)
		if err == nil {
			t.Fatal("expected an error without WithAllowDML")
		}
		if !strings.Contains(err.Error(), "data-modifying") {
			t.Errorf("error = %v, want it to mention a data-modifying statement", err)
		}
	})
}

// Regression: MySQL and MariaDB reject every statement inside a READ ONLY
// transaction with error 1792 — including an EXPLAIN that only plans it. So
// WithAllowDML must open a regular transaction and rely on the rollback, which
// this test also verifies by counting rows.
func TestMySQL_ExplainDMLDoesNotMutate(t *testing.T) {
	eachServer(t, func(t *testing.T, db *sql.DB) {
		p := analyzerFor(t, db, "mysql", explain.WithAllowDML())

		var before int
		if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&before); err != nil {
			t.Fatalf("count before: %v", err)
		}

		if _, err := p.Analyze(testCtx(t), `DELETE FROM users WHERE id < 100`); err != nil {
			t.Fatalf("Analyze(DELETE) with WithAllowDML: %v", err)
		}

		var after int
		if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&after); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if before != after {
			t.Errorf("row count changed from %d to %d; EXPLAIN must never mutate", before, after)
		}
	})
}

// Forcing @@explain_format=TREE reproduces the MySQL 9 default on any MySQL
// version, proving the FORMAT=TRADITIONAL clause overrides the session default
// rather than merely happening to agree with it. MariaDB has no such variable.
func TestMySQL_TraditionalFormatOverridesSessionDefault(t *testing.T) {
	db := setupMySQL(t, envMySQL)

	var original string
	if err := db.QueryRow(`SELECT @@explain_format`).Scan(&original); err != nil {
		t.Skipf("server has no @@explain_format: %v", err)
	}

	exec(t, db, `SET GLOBAL explain_format = 'TREE'`)
	t.Cleanup(func() { exec(t, db, `SET GLOBAL explain_format = '`+original+`'`) })

	// A fresh pool picks up the new global default on every new connection.
	fresh := connect(t, envMySQL, "mysql")
	var got string
	if err := fresh.QueryRow(`SELECT @@explain_format`).Scan(&got); err != nil {
		t.Fatalf("read @@explain_format: %v", err)
	}
	if got != "TREE" {
		t.Fatalf("@@explain_format = %q, want TREE; the premise of this test does not hold", got)
	}

	res, err := analyzerFor(t, fresh, "mysql").Analyze(testCtx(t), `SELECT * FROM users WHERE name = 'n1'`)
	if err != nil {
		t.Fatalf("Analyze under explain_format=TREE: %v", err)
	}
	if !hasRule(res.Issues, "full-table-scan") {
		t.Errorf("expected full-table-scan under explain_format=TREE, got %v", ruleNames(res.Issues))
	}
}
