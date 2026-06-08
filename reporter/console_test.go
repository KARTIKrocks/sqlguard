package reporter

import (
	"bytes"
	"strings"
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestConsoleReporter_Report(t *testing.T) {
	var buf bytes.Buffer
	rep := &ConsoleReporter{Out: &buf}

	results := []analyzer.Result{
		{
			RuleName:   "select-star",
			Severity:   analyzer.SeverityWarning,
			Query:      "SELECT * FROM users",
			Message:    "SELECT * detected.",
			Suggestion: "Select only needed columns.",
		},
	}

	rep.Report(results)
	output := buf.String()

	if !strings.Contains(output, "SQLGUARD WARNING") {
		t.Error("expected WARNING label in output")
	}
	if !strings.Contains(output, "select-star") {
		t.Error("expected rule name in output")
	}
	if !strings.Contains(output, "SELECT * FROM users") {
		t.Error("expected query in output")
	}
	if !strings.Contains(output, "Select only needed columns.") {
		t.Error("expected suggestion in output")
	}
}

func TestConsoleReporter_CriticalSeverity(t *testing.T) {
	var buf bytes.Buffer
	rep := &ConsoleReporter{Out: &buf}

	rep.Report([]analyzer.Result{{
		RuleName: "delete-without-where",
		Severity: analyzer.SeverityCritical,
		Query:    "DELETE FROM users",
		Message:  "DELETE without WHERE.",
	}})

	if !strings.Contains(buf.String(), "SQLGUARD CRITICAL") {
		t.Error("expected CRITICAL label in output")
	}
}

func TestConsoleReporter_WithFileInfo(t *testing.T) {
	var buf bytes.Buffer
	rep := &ConsoleReporter{Out: &buf}

	rep.Report([]analyzer.Result{{
		RuleName: "select-star",
		Severity: analyzer.SeverityWarning,
		Query:    "SELECT * FROM users",
		Message:  "SELECT * detected.",
		File:     "repo/user.go",
		Line:     42,
	}})

	output := buf.String()
	if !strings.Contains(output, "repo/user.go:42") {
		t.Error("expected file:line in output")
	}
}

func TestConsoleReporter_EmptyResults(t *testing.T) {
	var buf bytes.Buffer
	rep := &ConsoleReporter{Out: &buf}

	rep.Report(nil)

	if buf.Len() != 0 {
		t.Error("expected no output for empty results")
	}
}
