package analyzer

// Result represents a single finding from query analysis.
type Result struct {
	RuleName string
	Severity Severity
	// Query is the offending SQL as surfaced to reporters. By default it is
	// redacted (string/numeric literals replaced with "?") so literal values
	// never reach a log sink; an Analyzer built WithRawQuery leaves it raw.
	Query string
	// Fingerprint is the redacted, whitespace-collapsed, list-folded query
	// identity (see analyzer.Fingerprint). It is always set, never carries
	// PII, and is safe as a metric label or log key.
	Fingerprint string
	Message     string
	Suggestion  string
	File        string // populated only in static analysis mode
	Line        int    // populated only in static analysis mode
}
