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

// TestSeqScanSeverity_EscalationOnlyGoesUp drives walkPgPlan, the code that
// actually builds the finding. Asserting on a locally computed max would only
// exercise the builtin: it could not fail, and would keep passing if
// walkPgPlan went back to assigning SeverityWarning outright.
//
// analyzer.Register can replace a built-in by name, so a seq-scan registered
// above WARNING must not be *lowered* by the wide-scan branch.
func TestSeqScanSeverity_EscalationOnlyGoesUp(t *testing.T) {
	orig, ok := analyzer.RuleDefaultSeverity("seq-scan")
	if !ok {
		t.Fatal("seq-scan is not registered")
	}
	t.Cleanup(func() {
		analyzer.Register(analyzer.RuleSpec{Name: "seq-scan", DefaultSeverity: orig})
	})
	analyzer.Register(analyzer.RuleSpec{Name: "seq-scan", DefaultSeverity: analyzer.SeverityCritical})

	pa := &PlanAnalyzer{}

	for _, tc := range []struct {
		name string
		rows int64
	}{
		{"narrow scan reports the registered severity", 10},
		{"wide scan must not drop below it", 500_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var issues []analyzer.Result
			pa.walkPgPlan(&pgPlanNode{NodeType: "Seq Scan", PlanRows: tc.rows}, "SELECT 1", &issues)

			var seq *analyzer.Result
			for i := range issues {
				if issues[i].RuleName == "seq-scan" {
					seq = &issues[i]
				}
			}
			if seq == nil {
				t.Fatalf("no seq-scan finding for a Seq Scan node: %+v", issues)
			}
			if seq.Severity < analyzer.SeverityCritical {
				t.Errorf("reported %v, below the registered CRITICAL", seq.Severity)
			}
		})
	}
}

// TestSeqScanSeverity_EscalatesFromTheRegisteredDefault is the other
// direction: at the built-in INFO, a wide scan must still be raised.
func TestSeqScanSeverity_EscalatesFromTheRegisteredDefault(t *testing.T) {
	pa := &PlanAnalyzer{}

	var narrow, wide []analyzer.Result
	pa.walkPgPlan(&pgPlanNode{NodeType: "Seq Scan", PlanRows: 10}, "SELECT 1", &narrow)
	pa.walkPgPlan(&pgPlanNode{NodeType: "Seq Scan", PlanRows: 500_000}, "SELECT 1", &wide)

	if len(narrow) == 0 || len(wide) == 0 {
		t.Fatalf("expected a finding from each: narrow=%+v wide=%+v", narrow, wide)
	}
	if narrow[0].Severity != analyzer.SeverityInfo {
		t.Errorf("narrow scan = %v, want the registered INFO", narrow[0].Severity)
	}
	if wide[0].Severity != analyzer.SeverityWarning {
		t.Errorf("wide scan = %v, want WARNING", wide[0].Severity)
	}
}
