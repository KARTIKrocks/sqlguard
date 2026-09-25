package analyzer

import "maps"

// Rule checks a normalized Statement and returns a Result if an issue is
// found. It returns the result and true if an issue was detected, or a zero
// Result and false otherwise.
//
// Rules operate on the parsed Statement, not the raw SQL string, so a query
// is parsed once per Analyze call and every rule sees the same dialect-
// agnostic view.
type Rule func(s *Statement) (Result, bool)

// boundRule is a rule together with its registry name and the default
// severity from its RuleSpec. The name is "" for rules supplied directly via
// New (anonymous rules); profile overrides and suppressions only apply to
// named, registry-built rules. hasSeverity distinguishes a registry-built rule
// (whose severity is the spec's DefaultSeverity, the single source of truth)
// from an anonymous rule (which carries its own severity in the Result it
// returns); since SeverityInfo is the zero value, a flag is needed rather than
// a sentinel.
type boundRule struct {
	name        string
	check       Rule
	severity    Severity
	hasSeverity bool
}

// Analyzer holds a set of rules and a Parser, and runs the rules against
// SQL queries. Configuration (disabled rules, severity overrides, per-rule
// settings) is resolved once at construction into the bound rule set and the
// severity map; the per-query Analyze path does no config work.
type Analyzer struct {
	rules    []boundRule
	parser   Parser
	severity map[string]Severity
	// disabled holds the rules turned off by name — `disable:`, or
	// `severity: off`. It is what RuleEnabled answers from, and it does not
	// fold in the `only` whitelist; see RuleEnabled for why.
	disabled map[string]bool
	// settings holds per-rule tunables for the same audience; the statement
	// rules have theirs baked in by their factory at construction.
	settings map[string]Settings
	// rawQuery, when true, leaves Result.Query unredacted. Default is false
	// (redact): the safe default for a tool whose findings flow into logs.
	rawQuery bool
}

// New creates an Analyzer with the given anonymous rules, using the
// zero-dependency FallbackParser. Use WithParser to supply a real dialect
// parser. Rules added this way are not subject to profile overrides (they
// have no registry name); use Default/DefaultWithProfile for configurable
// built-in rules.
func New(rules ...Rule) *Analyzer {
	bound := make([]boundRule, len(rules))
	for i, r := range rules {
		bound[i] = boundRule{check: r}
	}
	return &Analyzer{rules: bound, parser: NewFallbackParser()}
}

// WithParser returns a copy of the Analyzer that uses the given Parser.
// Passing nil resets it to the FallbackParser.
func (a *Analyzer) WithParser(p Parser) *Analyzer {
	if p == nil {
		p = NewFallbackParser()
	}
	cp := *a
	cp.parser = p
	return &cp
}

// WithRawQuery returns a copy of the Analyzer that leaves Result.Query
// unredacted (the raw SQL, literals and all). Redaction is on by default so
// literal values never reach a log sink; opt out only for local debugging
// where the query text is trusted. Fingerprint is always populated either
// way.
func (a *Analyzer) WithRawQuery() *Analyzer {
	cp := *a
	cp.rawQuery = true
	return &cp
}

// PrepareQuery returns the query field and fingerprint for a Result built
// outside the rule path (e.g. the runtime slow-query and N+1 findings),
// applying the same redaction policy as Analyze so every emitted Result is
// consistent. display is redacted unless the Analyzer was built
// WithRawQuery; fingerprint is always the PII-free identity.
func (a *Analyzer) PrepareQuery(raw string) (display, fingerprint string) {
	// One redaction pass feeds both returns: the fingerprint is the redacted
	// text folded further, so computing it via Fingerprint(raw) would redact
	// the same query a second time on a per-query path.
	redacted := Redact(raw)
	fingerprint = foldRedacted(redacted)
	if a.rawQuery {
		return raw, fingerprint
	}
	return redacted, fingerprint
}

// Default creates an Analyzer with all registered built-in rules and the
// fallback parser, using each rule's default settings and severity.
func Default() *Analyzer {
	return DefaultWithProfile(Profile{})
}

// DefaultWithProfile builds an Analyzer from the rule registry with the given
// Profile applied: disabled/whitelisted rules are filtered, per-rule settings
// are passed to each rule's factory, and severity overrides are precomputed.
// The config package uses this to turn a .sqlguard.yml into an Analyzer
// without analyzer ever importing config or YAML.
func DefaultWithProfile(p Profile) *Analyzer {
	all := specs()
	var bound []boundRule
	disabled := make(map[string]bool, len(p.Disabled))
	for _, spec := range all {
		if p.Disabled[spec.Name] {
			disabled[spec.Name] = true
		}
		if p.Skip(spec.Name) {
			continue
		}
		// Registered for addressability only — middleware and explain build
		// these findings themselves and consult the decisions below.
		if !spec.Evaluated() {
			continue
		}
		bound = append(bound, boundRule{
			name:        spec.Name,
			check:       spec.Factory(p.Settings[spec.Name]),
			severity:    spec.DefaultSeverity,
			hasSeverity: true,
		})
	}
	var sev map[string]Severity
	if len(p.Severity) > 0 {
		sev = make(map[string]Severity, len(p.Severity))
		maps.Copy(sev, p.Severity)
	}
	var settings map[string]Settings
	if len(p.Settings) > 0 {
		settings = make(map[string]Settings, len(p.Settings))
		maps.Copy(settings, p.Settings)
	}
	return &Analyzer{
		rules:    bound,
		parser:   NewFallbackParser(),
		severity: sev,
		disabled: disabled,
		settings: settings,
		rawQuery: p.RawQuery,
	}
}

// RuleEnabled reports whether the profile leaves the named rule on, for a
// finding built outside the statement path: middleware's `slow-query` and
// `n-plus-one`, and the plan rules `explain` derives. Those have no Factory,
// so the Analyzer never runs them and their owners ask here instead.
//
// It answers `disable:` and `severity: off`. An `only:` whitelist is
// deliberately **not** consulted: `only:` selects which rules the Analyzer
// evaluates over a statement, and these are not evaluated at all. A list
// written to focus `sqlguard scan` — overwhelmingly what `only:` is for —
// would otherwise switch off slow-query and N+1 in a running application and
// blank out `sqlguard explain`, none of which it mentions. Turning one of
// these off takes naming it.
//
// For an evaluated rule the whitelist has already been applied: a rule it
// excludes was never bound, so nothing asks this about it.
//
// An unregistered name is reported as enabled. An Analyzer built with New has
// no profile, and a caller's own rule is not the profile's to turn off.
func (a *Analyzer) RuleEnabled(name string) bool { return !a.disabled[name] }

// RuleSeverity returns the severity to report for name, applying a profile
// override to def when one is set.
func (a *Analyzer) RuleSeverity(name string, def Severity) Severity {
	if a.severity != nil {
		if s, has := a.severity[name]; has {
			return s
		}
	}
	return def
}

// RuleSettings returns the profile settings for name, or nil when none were
// configured. Settings.Int / .Duration treat a nil Settings as "use the
// default", so a caller can read straight through without a nil check.
func (a *Analyzer) RuleSettings(name string) Settings { return a.settings[name] }

// Analyze parses the query once and runs all rules against it. If the
// configured parser returns an error, it degrades to the FallbackParser so
// analysis never breaks the caller's query path. Findings for rules named in
// an in-SQL `sqlguard:ignore` directive are suppressed, and severity
// overrides from the active Profile are applied.
func (a *Analyzer) Analyze(query string) []Result {
	stmt, err := a.parser.Parse(query)
	if err != nil || stmt == nil {
		stmt, _ = NewFallbackParser().Parse(query)
	}

	ignoreAll, ignored := parseIgnoreDirective(query)

	display, fingerprint := a.PrepareQuery(query)

	results := make([]Result, 0, len(a.rules))
	for _, br := range a.rules {
		if ignoreAll {
			break
		}
		r, ok := br.check(stmt)
		if !ok {
			continue
		}
		if r.RuleName != "" && ignored[r.RuleName] {
			continue
		}
		// Severity precedence: the spec's DefaultSeverity is the single source
		// of truth for a registry-built rule (the rule body no longer sets
		// one); a profile override, when present, wins over that.
		if br.hasSeverity {
			r.Severity = br.severity
		}
		if a.severity != nil {
			if s, has := a.severity[r.RuleName]; has {
				r.Severity = s
			}
		}
		r.Query = display
		r.Fingerprint = fingerprint
		results = append(results, r)
	}
	return results
}
