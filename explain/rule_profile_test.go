package explain

import (
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// The five plan rules are registered in the analyzer purely so they are
// addressable; explain borrows the resolved decisions. These pin that
// `disable`, `only` and `severity` reach a plan finding the same way they
// reach a statement rule.

func planIssues() []analyzer.Result {
	return []analyzer.Result{
		{RuleName: "seq-scan", Severity: analyzer.SeverityInfo, Message: "seq"},
		{RuleName: "high-cost", Severity: analyzer.SeverityWarning, Message: "cost"},
		{RuleName: "filesort", Severity: analyzer.SeverityInfo, Message: "sort"},
	}
}

func TestApplyProfile_NilLeavesIssuesAlone(t *testing.T) {
	p := &PlanAnalyzer{}
	got := p.applyProfile(planIssues())
	if len(got) != 3 {
		t.Errorf("an unconfigured analyzer should not filter: %+v", got)
	}
}

func TestApplyProfile_Disable(t *testing.T) {
	p := &PlanAnalyzer{rules: analyzer.DefaultWithProfile(analyzer.Profile{
		Disabled: map[string]bool{"seq-scan": true, "filesort": true},
	})}

	got := p.applyProfile(planIssues())

	if len(got) != 1 || got[0].RuleName != "high-cost" {
		t.Errorf("expected only high-cost to survive, got %+v", got)
	}
}

// TestApplyProfile_IgnoresOnly pins the asymmetry. `only:` is overwhelmingly
// written to focus `sqlguard scan`, and it selects which rules run over a
// statement; letting it reach here would mean a config that never mentions
// EXPLAIN silently turns `sqlguard explain` into a command that always reports
// nothing. Turning a plan rule off takes naming it in `disable:`.
func TestApplyProfile_IgnoresOnly(t *testing.T) {
	p := &PlanAnalyzer{rules: analyzer.DefaultWithProfile(analyzer.Profile{
		Only: map[string]bool{"select-star": true},
	})}

	got := p.applyProfile(planIssues())

	if len(got) != 3 {
		t.Errorf("an `only` whitelist should not filter plan findings, got %+v", got)
	}
}

// TestApplyProfile_DisableWinsInsideAnOnlyList is the escape hatch: `only:`
// does not reach a plan rule, but naming one in `disable:` still does, even
// alongside a whitelist.
func TestApplyProfile_DisableWinsInsideAnOnlyList(t *testing.T) {
	p := &PlanAnalyzer{rules: analyzer.DefaultWithProfile(analyzer.Profile{
		Only:     map[string]bool{"select-star": true},
		Disabled: map[string]bool{"high-cost": true},
	})}

	got := p.applyProfile(planIssues())

	if len(got) != 2 {
		t.Fatalf("expected the other two to survive, got %+v", got)
	}
	for _, r := range got {
		if r.RuleName == "high-cost" {
			t.Error("an explicit disable should still drop the rule")
		}
	}
}

// TestApplyProfile_SeverityOverridesComputed matters for seq-scan, whose
// severity is derived from the estimated row count. An explicit setting has
// to outrank that, not be outranked by it.
func TestApplyProfile_SeverityOverridesComputed(t *testing.T) {
	p := &PlanAnalyzer{rules: analyzer.DefaultWithProfile(analyzer.Profile{
		Severity: map[string]analyzer.Severity{"seq-scan": analyzer.SeverityCritical},
	})}

	issues := []analyzer.Result{
		{RuleName: "seq-scan", Severity: analyzer.SeverityWarning}, // computed from PlanRows > 1000
	}
	got := p.applyProfile(issues)

	if len(got) != 1 || got[0].Severity != analyzer.SeverityCritical {
		t.Errorf("the override should beat the row-count severity, got %+v", got)
	}
}

func TestApplyProfile_SeverityOffDisables(t *testing.T) {
	// config translates `severity: off` into Disabled, so this is the shape
	// explain receives for an "off" plan rule.
	p := &PlanAnalyzer{rules: analyzer.DefaultWithProfile(analyzer.Profile{
		Disabled: map[string]bool{"high-cost": true},
	})}

	for _, r := range p.applyProfile(planIssues()) {
		if r.RuleName == "high-cost" {
			t.Error("high-cost should have been dropped")
		}
	}
}
