package analyzer

import (
	"slices"
	"testing"
)

// TestNonEvaluatedRulesAreRegisteredButNotRun pins both halves of the
// arrangement: the runtime and plan findings must be addressable by name so
// config can disable or re-severity them, while never being constructed or
// run against a Statement — they have no Factory and nothing to evaluate.
func TestNonEvaluatedRulesAreRegisteredButNotRun(t *testing.T) {
	nonEvaluated := []string{
		"slow-query", "n-plus-one",
		"seq-scan", "high-cost", "full-table-scan", "no-index-used", "filesort",
	}

	names := RuleNames()
	for _, want := range nonEvaluated {
		if !slices.Contains(names, want) {
			t.Errorf("%q is not registered, so config cannot address it", want)
		}
	}

	a := Default()
	for _, br := range a.rules {
		if slices.Contains(nonEvaluated, br.name) {
			t.Errorf("%q was built as a statement rule; it has nothing to evaluate", br.name)
		}
	}

	// A query that trips a statement rule must not gain phantom findings.
	for _, r := range a.Analyze("SELECT * FROM users") {
		if slices.Contains(nonEvaluated, r.RuleName) {
			t.Errorf("Analyze produced %q, which it cannot evaluate", r.RuleName)
		}
	}
}

// TestRuleEnabledAnswersForEveryRegisteredRule is what middleware and explain
// depend on: a name they never ask the Analyzer to run must still get an
// honest enabled/disabled answer.
func TestRuleEnabledAnswersForEveryRegisteredRule(t *testing.T) {
	a := DefaultWithProfile(Profile{Disabled: map[string]bool{"slow-query": true, "seq-scan": true}})

	if a.RuleEnabled("slow-query") {
		t.Error("slow-query should be reported disabled")
	}
	if a.RuleEnabled("seq-scan") {
		t.Error("seq-scan should be reported disabled")
	}
	if !a.RuleEnabled("n-plus-one") {
		t.Error("n-plus-one was not disabled and should stay enabled")
	}
	if !a.RuleEnabled("select-star") {
		t.Error("select-star was not disabled and should stay enabled")
	}
	// An unknown name is not the profile's to turn off.
	if !a.RuleEnabled("somebody-elses-rule") {
		t.Error("an unregistered rule should be reported enabled")
	}
}

// TestRuleEnabledIgnoresOnly pins the semantic that makes `only:` mean one
// thing everywhere: it selects which rules the Analyzer evaluates over a
// statement, and the seven findings built outside that path are not evaluated,
// so a whitelist written to focus `sqlguard scan` does not reach them.
func TestRuleEnabledIgnoresOnly(t *testing.T) {
	a := DefaultWithProfile(Profile{Only: map[string]bool{"select-star": true}})

	for _, name := range []string{
		"slow-query", "n-plus-one",
		"seq-scan", "high-cost", "full-table-scan", "no-index-used", "filesort",
	} {
		if !a.RuleEnabled(name) {
			t.Errorf("an only whitelist should not reach %q", name)
		}
	}

	// The whitelist still does its job for the rules it is about.
	if len(a.rules) != 1 || a.rules[0].name != "select-star" {
		t.Errorf("expected only select-star bound, got %d rules", len(a.rules))
	}
}

func TestRuleEnabledHonoursDisable(t *testing.T) {
	a := DefaultWithProfile(Profile{Disabled: map[string]bool{"seq-scan": true}})

	if a.RuleEnabled("seq-scan") {
		t.Error("seq-scan was named in disable: and should be reported off")
	}
	if !a.RuleEnabled("filesort") {
		t.Error("filesort was not named and should stay on")
	}
	// An unregistered name is nobody's to turn off.
	if !a.RuleEnabled("somebody-elses-rule") {
		t.Error("an unregistered rule should be reported enabled")
	}
}

// TestRuleEnabledHonoursDisableInsideAnOnlyList is the escape hatch: `only:`
// does not reach these findings, but naming one in `disable:` still does.
func TestRuleEnabledHonoursDisableInsideAnOnlyList(t *testing.T) {
	a := DefaultWithProfile(Profile{
		Only:     map[string]bool{"select-star": true},
		Disabled: map[string]bool{"slow-query": true},
	})

	if a.RuleEnabled("slow-query") {
		t.Error("an explicit disable should still switch slow-query off")
	}
	if !a.RuleEnabled("n-plus-one") {
		t.Error("n-plus-one was not named and should stay on")
	}
}

// TestRuleDefaultSeverityCoversNonEvaluatedRules makes the registry entries
// for the seven load-bearing. Nothing constructs them, so without a reader
// their DefaultSeverity would be decorative and the literals at each build
// site would silently outrank it.
func TestRuleDefaultSeverityCoversNonEvaluatedRules(t *testing.T) {
	want := map[string]Severity{
		"slow-query":      SeverityWarning,
		"n-plus-one":      SeverityWarning,
		"seq-scan":        SeverityInfo,
		"high-cost":       SeverityWarning,
		"full-table-scan": SeverityWarning,
		"no-index-used":   SeverityWarning,
		"filesort":        SeverityInfo,
	}
	for name, sev := range want {
		got, ok := RuleDefaultSeverity(name)
		if !ok {
			t.Errorf("%q is not registered", name)
			continue
		}
		if got != sev {
			t.Errorf("%q default severity = %v, want %v", name, got, sev)
		}
	}

	if _, ok := RuleDefaultSeverity("somebody-elses-rule"); ok {
		t.Error("an unregistered name should report ok=false")
	}
}

// TestEvaluatedRuleNamesExcludesTheSeven is what lets config tell an `only:`
// list that narrows the scan from one that leaves it with nothing to run.
func TestEvaluatedRuleNamesExcludesTheSeven(t *testing.T) {
	evaluated := EvaluatedRuleNames()
	for _, name := range []string{
		"slow-query", "n-plus-one",
		"seq-scan", "high-cost", "full-table-scan", "no-index-used", "filesort",
	} {
		if slices.Contains(evaluated, name) {
			t.Errorf("%q has no Factory and should not be listed as evaluated", name)
		}
	}
	if !slices.Contains(evaluated, "select-star") {
		t.Error("select-star is evaluated and should be listed")
	}
	if len(evaluated) != len(RuleNames())-7 {
		t.Errorf("evaluated=%d, registered=%d; expected exactly 7 non-evaluated",
			len(evaluated), len(RuleNames()))
	}
}
