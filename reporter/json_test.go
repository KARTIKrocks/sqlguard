package reporter

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestJSONReporter_Report(t *testing.T) {
	var buf bytes.Buffer
	rep := &JSONReporter{Out: &buf}

	results := []analyzer.Result{
		{
			RuleName:   "select-star",
			Severity:   analyzer.SeverityWarning,
			Query:      "SELECT * FROM users",
			Message:    "SELECT * detected.",
			Suggestion: "Select only needed columns.",
			File:       "user.go",
			Line:       10,
		},
		{
			RuleName: "delete-without-where",
			Severity: analyzer.SeverityCritical,
			Query:    "DELETE FROM users",
			Message:  "DELETE without WHERE.",
		},
	}

	rep.Report(results)

	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v\nGot: %s", err, buf.String())
	}

	if len(parsed) != 2 {
		t.Fatalf("expected 2 results, got %d", len(parsed))
	}

	if parsed[0]["rule"] != "select-star" {
		t.Errorf("expected rule 'select-star', got %v", parsed[0]["rule"])
	}
	if parsed[0]["severity"] != "WARNING" {
		t.Errorf("expected severity 'WARNING', got %v", parsed[0]["severity"])
	}
	if parsed[0]["file"] != "user.go" {
		t.Errorf("expected file 'user.go', got %v", parsed[0]["file"])
	}

	if parsed[1]["severity"] != "CRITICAL" {
		t.Errorf("expected severity 'CRITICAL', got %v", parsed[1]["severity"])
	}
	// file should be omitted (empty)
	if _, ok := parsed[1]["file"]; ok && parsed[1]["file"] != "" {
		t.Errorf("expected file to be omitted, got %v", parsed[1]["file"])
	}
}

func TestJSONReporter_EmptyResults(t *testing.T) {
	var buf bytes.Buffer
	rep := &JSONReporter{Out: &buf}

	rep.Report([]analyzer.Result{})

	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	if len(parsed) != 0 {
		t.Errorf("expected empty array, got %d items", len(parsed))
	}
}
