package analyzer

import (
	"slices"
	"testing"
)

func TestRuleNamesCoversBuiltins(t *testing.T) {
	names := RuleNames()
	for _, want := range []string{
		"select-star", "leading-wildcard", "delete-without-where",
		"update-without-where", "insert-without-columns",
		"select-without-limit", "orderby-without-limit",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("rule %q not registered; got %v", want, names)
		}
	}
}

func TestDefaultMatchesRegistry(t *testing.T) {
	// Default() must behave exactly as before the registry refactor.
	a := Default()
	if got := a.Analyze("DELETE FROM users"); len(got) == 0 || got[0].Severity != SeverityCritical {
		t.Fatalf("expected critical delete-without-where, got %+v", got)
	}
	if got := a.Analyze("SELECT id FROM users WHERE id = 1"); len(got) != 0 {
		t.Errorf("expected no findings, got %+v", got)
	}
}

func TestProfileDisable(t *testing.T) {
	a := DefaultWithProfile(Profile{Disabled: map[string]bool{"select-star": true}})
	for _, r := range a.Analyze("SELECT * FROM users") {
		if r.RuleName == "select-star" {
			t.Fatal("select-star should be disabled")
		}
	}
}

func TestProfileOnlyWhitelist(t *testing.T) {
	a := DefaultWithProfile(Profile{Only: map[string]bool{"select-star": true}})
	// delete-without-where must not run; only select-star is whitelisted.
	got := a.Analyze("DELETE FROM users")
	if len(got) != 0 {
		t.Errorf("expected no findings with whitelist, got %+v", got)
	}
	if got := a.Analyze("SELECT * FROM users"); len(got) != 1 || got[0].RuleName != "select-star" {
		t.Errorf("expected only select-star, got %+v", got)
	}
}

func TestProfileSeverityOverride(t *testing.T) {
	a := DefaultWithProfile(Profile{Severity: map[string]Severity{"select-star": SeverityInfo}})
	got := a.Analyze("SELECT * FROM users")
	if len(got) == 0 || got[0].RuleName != "select-star" || got[0].Severity != SeverityInfo {
		t.Fatalf("expected select-star downgraded to INFO, got %+v", got)
	}
}

func TestProfileSettingsLeadingWildcardMinLength(t *testing.T) {
	a := DefaultWithProfile(Profile{
		Settings: map[string]Settings{"leading-wildcard": {"min-length": 5}},
	})
	// 2-char term -> below threshold, not flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t WHERE x LIKE '%ab%'"), "leading-wildcard"); hits != 0 {
		t.Errorf("short pattern should be tolerated with min-length=5")
	}
	// 6-char term -> flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t WHERE x LIKE '%abcdef%'"), "leading-wildcard"); hits != 1 {
		t.Errorf("long pattern should still be flagged with min-length=5")
	}
}

func TestProfileSettingsInListMaxLength(t *testing.T) {
	a := DefaultWithProfile(Profile{
		Settings: map[string]Settings{"in-list-too-large": {"max-length": 3}},
	})
	// 3 elements -> at threshold, not flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t WHERE id IN (1, 2, 3)"), "in-list-too-large"); hits != 0 {
		t.Errorf("list at threshold should be tolerated with max-length=3")
	}
	// 4 elements -> over threshold, flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t WHERE id IN (1, 2, 3, 4)"), "in-list-too-large"); hits != 1 {
		t.Errorf("list over threshold should be flagged with max-length=3")
	}
}

func TestProfileSettingsLargeOffsetThreshold(t *testing.T) {
	a := DefaultWithProfile(Profile{
		Settings: map[string]Settings{"large-offset": {"threshold": 100}},
	})
	// offset 100 -> at threshold, not flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t LIMIT 10 OFFSET 100"), "large-offset"); hits != 0 {
		t.Errorf("offset at threshold should be tolerated with threshold=100")
	}
	// offset 200 -> over threshold, flagged.
	if hits := filterByRule(a.Analyze("SELECT id FROM t LIMIT 10 OFFSET 200"), "large-offset"); hits != 1 {
		t.Errorf("offset over threshold should be flagged with threshold=100")
	}
}

func TestInlineSuppression(t *testing.T) {
	a := Default()

	if got := a.Analyze("SELECT * FROM users -- sqlguard:ignore"); len(got) != 0 {
		t.Errorf("bare ignore should suppress all, got %+v", got)
	}
	// Scoped: suppress select-star only; delete-without-where still fires.
	q := "DELETE FROM users /* sqlguard:ignore:select-star */"
	if got := a.Analyze(q); len(got) != 1 || got[0].RuleName != "delete-without-where" {
		t.Errorf("scoped ignore should keep delete-without-where, got %+v", got)
	}
	if got := a.Analyze("SELECT * FROM users WHERE id = 1 /* sqlguard:ignore:select-star */"); len(got) != 0 {
		t.Errorf("select-star should be suppressed, got %+v", got)
	}
	// The token inside a string literal must NOT suppress (no comment marker).
	if got := a.Analyze("SELECT * FROM users WHERE note = 'sqlguard:ignore'"); len(got) == 0 {
		t.Error("string-literal text must not act as a suppression directive")
	}
}

func TestParseIgnoreComment(t *testing.T) {
	if all, _, found := ParseIgnoreComment("// sqlguard:ignore"); !found || !all {
		t.Error("expected bare directive parsed as all")
	}
	all, rules, found := ParseIgnoreComment("// noise sqlguard:ignore:select-star, leading-wildcard")
	if !found || all || !rules["select-star"] || !rules["leading-wildcard"] {
		t.Errorf("expected scoped rules, got all=%v rules=%v found=%v", all, rules, found)
	}
	if _, _, found := ParseIgnoreComment("// just a normal comment"); found {
		t.Error("non-directive comment should not be found")
	}
}

func filterByRule(rs []Result, name string) int {
	n := 0
	for _, r := range rs {
		if r.RuleName == name {
			n++
		}
	}
	return n
}
