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

// TestOnlyWhitelistReachesNonEvaluatedRules covers the semantic `only` now
// carries: it is a whitelist across every surface, so a runtime or plan
// finding not named in it is off.
func TestOnlyWhitelistReachesNonEvaluatedRules(t *testing.T) {
	a := DefaultWithProfile(Profile{Only: map[string]bool{"select-star": true}})

	if a.RuleEnabled("slow-query") {
		t.Error("an `only` whitelist should exclude slow-query")
	}
	if !a.RuleEnabled("select-star") {
		t.Error("the whitelisted rule should stay enabled")
	}
}
