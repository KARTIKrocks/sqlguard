package middleware

import (
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

type mysqlOnlyParser struct{ analyzer.FallbackParser }

func (*mysqlOnlyParser) Dialect() analyzer.Dialect { return analyzer.DialectMySQL }

// TestGuardDialectPrecedence pins WithDialect > the analyzer's own (config
// file, then parser) > detected.
func TestGuardDialectPrecedence(t *testing.T) {
	pg, my := analyzer.DialectPostgres, analyzer.DialectMySQL
	fromConfig := analyzer.DefaultWithProfile(analyzer.Profile{Dialect: pg})
	for _, c := range []struct {
		name string
		opts []Option
		want analyzer.Dialect
	}{
		{"none", nil, analyzer.DialectUnknown},
		{"detected", []Option{WithDetectedDialect(pg)}, pg},
		{"unknown detection keeps a known one", []Option{WithDetectedDialect(pg), WithDetectedDialect(analyzer.DialectUnknown)}, pg},
		{"parser beats detected", []Option{WithDetectedDialect(pg), WithParser(&mysqlOnlyParser{})}, my},
		{"config beats parser", []Option{WithAnalyzer(fromConfig), WithParser(&mysqlOnlyParser{})}, pg},
		{"config beats detected", []Option{WithAnalyzer(fromConfig), WithDetectedDialect(my)}, pg},
		{"WithDialect beats config", []Option{WithAnalyzer(fromConfig), WithDialect(my)}, my},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := NewGuard(c.opts...).Analyzer().Dialect(); got != c.want {
				t.Errorf("dialect = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDriverDialect(t *testing.T) {
	for _, c := range []struct {
		name string
		drv  any
		want analyzer.Dialect
	}{
		{"pgx stdlib", &stdlib.Driver{}, analyzer.DialectPostgres},
		{"go-sql-driver/mysql", mysql.MySQLDriver{}, analyzer.DialectMySQL},
		{"unlisted", &wDriver{}, analyzer.DialectUnknown},
		{"nil", nil, analyzer.DialectUnknown},
	} {
		if got := driverDialect(c.drv); got != c.want {
			t.Errorf("%s: driverDialect = %v, want %v", c.name, got, c.want)
		}
	}
	// WrapDriver feeds the detection in, below a caller's own choice.
	g := WrapDriver(&stdlib.Driver{}).(*wDriver).g
	if d := g.Analyzer().Dialect(); d != analyzer.DialectPostgres {
		t.Errorf("WrapDriver(pgx) dialect = %v, want postgres", d)
	}
	g = WrapDriver(&stdlib.Driver{}, WithDetectedDialect(analyzer.DialectMySQL)).(*wDriver).g
	if d := g.Analyzer().Dialect(); d != analyzer.DialectMySQL {
		t.Errorf("caller's detected dialect lost to the driver's: %v", d)
	}
}

// TestN1GroupsByTheGuardsDialect pins that the N+1 key and its reported query
// come from the guard's analyzer, so a MySQL "…" value never reaches them.
func TestN1GroupsByTheGuardsDialect(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithDialect(analyzer.DialectMySQL), WithReporter(rep),
		WithN1Detection(2, time.Minute), WithFindingDedup(0))
	for _, name := range []string{"alice", "bob"} {
		g.Check(`SELECT id FROM t WHERE name = "` + name + `"`)
	}
	var n1 int
	for _, r := range rep.results {
		for _, v := range []string{r.Query, r.Fingerprint} {
			if strings.Contains(v, "alice") || strings.Contains(v, "bob") {
				t.Errorf("%s leaked a value: %q", r.RuleName, v)
			}
		}
		if r.RuleName == "n-plus-one" {
			n1++
		}
	}
	if n1 != 1 {
		t.Errorf("n-plus-one findings = %d, want 1 (both values share a fingerprint)", n1)
	}
}
