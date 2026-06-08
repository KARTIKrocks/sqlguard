package analyzer

// Severity represents the importance level of an analysis finding.
type Severity int

const (
	// SeverityInfo is an advisory finding worth noting but not necessarily acting on.
	SeverityInfo Severity = iota
	// SeverityWarning is a likely problem that should be reviewed.
	SeverityWarning
	// SeverityCritical is a serious problem likely to cause incorrect or destructive behavior.
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"
	case SeverityWarning:
		return "WARNING"
	case SeverityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}
