package reporter

import (
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// JSONReporter outputs analysis results as JSON.
type JSONReporter struct {
	Out io.Writer
	mu  sync.Mutex
}

// NewJSONReporter creates a JSONReporter that writes to stderr.
func NewJSONReporter() *JSONReporter {
	return &JSONReporter{Out: os.Stderr}
}

type jsonResult struct {
	Rule        string `json:"rule"`
	Severity    string `json:"severity"`
	Query       string `json:"query"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion,omitempty"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
}

// Report writes the results to the configured output as a JSON array.
func (j *JSONReporter) Report(results []analyzer.Result) {
	j.mu.Lock()
	defer j.mu.Unlock()

	out := make([]jsonResult, len(results))
	for i, r := range results {
		out[i] = jsonResult{
			Rule:        r.RuleName,
			Severity:    r.Severity.String(),
			Query:       r.Query,
			Fingerprint: r.Fingerprint,
			Message:     r.Message,
			Suggestion:  r.Suggestion,
			File:        r.File,
			Line:        r.Line,
		}
	}

	enc := json.NewEncoder(j.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		// Fallback: log encoding failure since Reporter interface can't return error
		fmt.Fprintf(os.Stderr, "sqlguard: failed to encode JSON report: %v\n", err)
	}
}
