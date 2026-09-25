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

// TestRejectedSettingDoesNotReachTheReader is the half the earlier fix missed.
// Rejecting a value under `strict: true` is the easy case — the load stops. In
// lenient mode, which is the default, the load continues, so a warning is only
// half an answer: `slow-query.threshold: 0` warned and then matched every
// successful query anyway, flooding the reporter it was meant to protect.
func TestRejectedSettingDoesNotReachTheReader(t *testing.T) {
	c := &Config{Rules: RulesConfig{Settings: map[string]map[string]any{
		"slow-query": {"threshold": 0},
	}}}

	p, err := c.Profile()
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if len(c.Warnings()) == 0 {
		t.Error("expected a warning for the zero threshold")
	}
	if _, present := p.Settings["slow-query"]["threshold"]; present {
		t.Errorf("the rejected value reached the profile: %v", p.Settings["slow-query"])
	}

	opts, err := c.MiddlewareOptions()
	if err != nil {
		t.Fatalf("MiddlewareOptions: %v", err)
	}
	var got []analyzer.Result
	opts = append(opts, middleware.WithReporter(
		reporterFunc(func(rs []analyzer.Result) { got = append(got, rs...) })))
	g := middleware.NewGuard(opts...)

	g.CheckLatency("SELECT id FROM t WHERE id = ?", time.Microsecond)

	if len(got) != 0 {
		t.Errorf("a 1µs query was reported slow, so the threshold fell to 0: %+v", got)
	}

	// The built-in default must be what stands in its place.
	g.CheckLatency("SELECT id FROM t WHERE id = ?", 300*time.Millisecond)
	if len(got) != 1 {
		t.Errorf("300ms should exceed the built-in 200ms default, got %+v", got)
	}
}
