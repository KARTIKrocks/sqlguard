package reporter

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

// ConsoleReporter prints analysis results to the terminal with color.
// The output writer is fixed at construction so Report is safe for concurrent
// use (the writer cannot be swapped out from under an in-flight Report).
type ConsoleReporter struct {
	out io.Writer
	mu  sync.Mutex
}

// NewConsoleReporter creates a ConsoleReporter that writes to stderr.
func NewConsoleReporter() *ConsoleReporter {
	return NewConsoleReporterTo(os.Stderr)
}

// NewConsoleReporterTo creates a ConsoleReporter that writes to w.
func NewConsoleReporterTo(w io.Writer) *ConsoleReporter {
	return &ConsoleReporter{out: w}
}

// Report writes each result to the configured output, colored by severity.
func (c *ConsoleReporter) Report(results []analyzer.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range results {
		color := colorCyan
		switch r.Severity {
		case analyzer.SeverityWarning:
			color = colorYellow
		case analyzer.SeverityCritical:
			color = colorRed
		}

		_, _ = fmt.Fprintf(c.out, "\n%s[SQLGUARD %s]%s %s\n", color, r.Severity, colorReset, r.RuleName)

		if r.File != "" {
			_, _ = fmt.Fprintf(c.out, "  File: %s:%d\n", r.File, r.Line)
		}

		_, _ = fmt.Fprintf(c.out, "  Query: %s\n", r.Query)
		_, _ = fmt.Fprintf(c.out, "  Issue: %s\n", r.Message)

		if r.Suggestion != "" {
			_, _ = fmt.Fprintf(c.out, "  Fix:   %s\n", r.Suggestion)
		}
	}
}
