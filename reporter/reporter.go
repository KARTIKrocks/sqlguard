package reporter

import "github.com/KARTIKrocks/sqlguard/analyzer"

// Reporter defines the interface for reporting analysis results.
type Reporter interface {
	Report(results []analyzer.Result)
}
