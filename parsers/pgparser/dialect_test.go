package pgparser

import (
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestParserSetsDialect(t *testing.T) {
	if d := analyzer.Default().WithParser(New()).Dialect(); d != analyzer.DialectPostgres {
		t.Errorf("dialect = %v, want %v", d, analyzer.DialectPostgres)
	}
}
