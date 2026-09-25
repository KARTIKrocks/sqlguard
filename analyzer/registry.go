package analyzer

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Settings holds rule-specific configuration as a generic key/value map so
// new tunables can be added without changing this type or the config schema.
// Accessors are nil-safe and fall back to the provided default, so a rule
// can always be constructed even with no settings supplied.
type Settings map[string]any

// Int returns the setting as an int, or def if missing or not numeric.
// YAML decodes integers as int and JSON as float64, so both are accepted.
func (s Settings) Int(key string, def int) int {
	if s == nil {
		return def
	}
	switch v := s[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return def
	}
}

// Bool returns the setting as a bool, or def if missing or not a bool.
func (s Settings) Bool(key string, def bool) bool {
	if s == nil {
		return def
	}
	if v, ok := s[key].(bool); ok {
		return v
	}
	return def
}

// String returns the setting as a string, or def if missing or not a string.
func (s Settings) String(key, def string) string {
	if s == nil {
		return def
	}
	if v, ok := s[key].(string); ok {
		return v
	}
	return def
}

// Duration returns the setting parsed as a time.Duration. It accepts a
// duration string ("200ms") or a number interpreted as milliseconds. Returns
// def if missing or unparseable.
func (s Settings) Duration(key string, def time.Duration) time.Duration {
	if s == nil {
		return def
	}
	switch v := s[key].(type) {
	case string:
		// Trimmed so this agrees with the config loader's validation; a
		// value it accepts must not fall back to def here.
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	case int:
		return time.Duration(v) * time.Millisecond
	case int64:
		return time.Duration(v) * time.Millisecond
	case float64:
		// Scale before converting: time.Duration(0.5) truncates to 0, which
		// turned a fractional-millisecond threshold into "no threshold".
		return time.Duration(v * float64(time.Millisecond))
	}
	return def
}

// RuleSpec describes a built-in rule: its stable name (used in config,
// suppressions and reports), its default severity, and a factory that builds
// the rule from its settings. Keeping construction behind a factory is what
// makes per-rule settings work uniformly for every present and future rule.
type RuleSpec struct {
	Name            string
	DefaultSeverity Severity
	// Factory builds the rule for the statement path. It is nil for findings
	// the Analyzer does not produce itself — middleware's `slow-query` and
	// `n-plus-one`, and the plan rules `explain` derives from a query plan.
	// Those register so they are addressable by name like any other rule;
	// their owners ask the Analyzer for the resolved decision before they
	// emit. A nil Factory is never called.
	Factory func(Settings) Rule
}

// Evaluated reports whether the Analyzer builds and runs this rule itself.
func (s RuleSpec) Evaluated() bool { return s.Factory != nil }

var (
	registryMu sync.RWMutex
	registry   = map[string]RuleSpec{}
)

// Register adds a rule to the global registry. Built-in rules call this from
// init(); third-party rules may call it too. A duplicate name overwrites the
// previous spec, so a custom rule can replace a built-in one by name.
func Register(spec RuleSpec) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[spec.Name] = spec
}

// RuleNames returns all registered rule names, sorted. Used by the config
// loader to validate rule references and by tooling to list rules.
func RuleNames() []string {
	registryMu.RLock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	registryMu.RUnlock()
	sort.Strings(names)
	return names
}

// EvaluatedRuleNames returns the rules the Analyzer runs against a Statement,
// sorted — the subset of RuleNames() that has a Factory. The config loader
// uses it to tell an `only:` list that selects nothing runnable from one that
// narrows the scan, which reads identically in YAML.
func EvaluatedRuleNames() []string {
	registryMu.RLock()
	names := make([]string, 0, len(registry))
	for n, spec := range registry {
		if spec.Evaluated() {
			names = append(names, n)
		}
	}
	registryMu.RUnlock()
	sort.Strings(names)
	return names
}

// RuleDefaultSeverity returns the severity a rule was registered with. ok is
// false for an unregistered name. The findings built outside the statement
// path read this rather than repeating a literal, so the registry entry stays
// the single source of truth for every rule, not just the evaluated ones.
func RuleDefaultSeverity(name string) (sev Severity, ok bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	spec, found := registry[name]
	if !found {
		return 0, false
	}
	return spec.DefaultSeverity, true
}

// specs returns all registered specs sorted by name, for deterministic
// analyzer construction and stable report ordering.
func specs() []RuleSpec {
	registryMu.RLock()
	out := make([]RuleSpec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Profile is the resolved, parser-independent view of configuration applied
// to an Analyzer at construction time. The config package builds it from
// .sqlguard.yml; analyzer never imports config or YAML. All maps are keyed
// by rule name. Resolution happens once here, never on the per-query path.
type Profile struct {
	// Disabled rules are not constructed or run.
	Disabled map[string]bool
	// Only, when non-empty, is a whitelist: only these rules run.
	Only map[string]bool
	// Severity overrides a rule's reported severity.
	Severity map[string]Severity
	// Settings holds per-rule tunables.
	Settings map[string]Settings
	// RawQuery, when true, disables Result.Query redaction (literals are
	// left in the reported SQL). Default (false) redacts — see
	// Analyzer.WithRawQuery.
	RawQuery bool
}

func (p Profile) skip(name string) bool {
	if len(p.Only) > 0 && !p.Only[name] {
		return true
	}
	return p.Disabled[name]
}
