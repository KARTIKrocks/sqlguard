package config

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard"
	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/KARTIKrocks/sqlguard/reporter"

	_ "github.com/mattn/go-sqlite3"
)

func TestMiddlewareOptionsAppliesProfile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "rules:\n  disable: [select-star]\n")

	opts, err := Middleware("", dir)
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}

	var buf strings.Builder
	opts = append(opts, middleware.WithReporter(reporter.NewConsoleReporterTo(&buf)))

	name := "sqlguard-cfg-test"
	if err := sqlguard.Register(name, "sqlite3", opts...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	db, err := sql.Open(name, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE u (id INTEGER, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, err := db.Query("SELECT * FROM u WHERE id = 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if strings.Contains(buf.String(), "select-star") {
		t.Errorf("select-star should be disabled via config, got:\n%s", buf.String())
	}
}

// TestOnlyDoesNotSilenceRuntimeFindings goes end to end from the YAML shape a
// repository actually ships: `only:` written to focus the scanner must not
// switch off the running application's latency reporting. The config package
// is where this can be asserted, since middleware cannot import it.
func TestOnlyDoesNotSilenceRuntimeFindings(t *testing.T) {
	c := &Config{Rules: RulesConfig{Only: []string{"select-star"}}}

	opts, err := c.MiddlewareOptions()
	if err != nil {
		t.Fatalf("MiddlewareOptions: %v", err)
	}

	var got []analyzer.Result
	opts = append(opts,
		middleware.WithReporter(reporterFunc(func(rs []analyzer.Result) { got = append(got, rs...) })),
		middleware.WithSlowQueryThreshold(time.Millisecond),
	)
	g := middleware.NewGuard(opts...)

	g.CheckLatency("SELECT id FROM t WHERE id = ?", time.Second)

	if len(got) != 1 || got[0].RuleName != "slow-query" {
		t.Errorf("an `only:` list silenced slow-query in the running app: %+v", got)
	}

	// The whitelist still narrows the statement rules it is about.
	a, err := c.Analyzer()
	if err != nil {
		t.Fatalf("Analyzer: %v", err)
	}
	for _, r := range a.Analyze("DELETE FROM sessions") {
		if r.RuleName != "select-star" {
			t.Errorf("only: [select-star] let %q through", r.RuleName)
		}
	}
}

type reporterFunc func([]analyzer.Result)

func (f reporterFunc) Report(rs []analyzer.Result) { f(rs) }
