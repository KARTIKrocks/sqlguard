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
	fingerprint = Fingerprint(raw)
	if a.rawQuery {
		return raw, fingerprint
	}
	return Redact(raw), fingerprint
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
	var bound []boundRule
	for _, spec := range specs() {
		if p.skip(spec.Name) {
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
	return &Analyzer{rules: bound, parser: NewFallbackParser(), severity: sev, rawQuery: p.RawQuery}
}

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
