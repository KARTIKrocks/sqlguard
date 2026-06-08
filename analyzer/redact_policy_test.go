package analyzer

import "testing"

func TestAnalyzeRedactsByDefault(t *testing.T) {
	q := `SELECT * FROM users WHERE email = 'alice@acme.com'`
	res := Default().Analyze(q)
	if len(res) == 0 {
		t.Fatal("expected at least one finding (select-star)")
	}
	for _, r := range res {
		if contains(r.Query, "alice@acme.com") {
			t.Errorf("default Analyze leaked literal in Query: %q", r.Query)
		}
		if r.Fingerprint == "" {
			t.Error("Fingerprint not populated")
		}
		if contains(r.Fingerprint, "alice@acme.com") {
			t.Errorf("Fingerprint leaked literal: %q", r.Fingerprint)
		}
	}
}

func TestWithRawQueryKeepsLiterals(t *testing.T) {
	q := `SELECT * FROM users WHERE email = 'alice@acme.com'`
	res := Default().WithRawQuery().Analyze(q)
	if len(res) == 0 {
		t.Fatal("expected a finding")
	}
	if !contains(res[0].Query, "alice@acme.com") {
		t.Errorf("WithRawQuery should keep raw SQL, got %q", res[0].Query)
	}
	if res[0].Fingerprint == "" {
		t.Error("Fingerprint must still be set in raw mode")
	}
}

func TestPrepareQueryPolicy(t *testing.T) {
	raw := `SELECT 'x' FROM t WHERE id = 9`
	d, fp := Default().PrepareQuery(raw)
	if contains(d, "'x'") {
		t.Errorf("default PrepareQuery should redact: %q", d)
	}
	if fp == "" {
		t.Error("fingerprint empty")
	}
	d2, _ := Default().WithRawQuery().PrepareQuery(raw)
	if d2 != raw {
		t.Errorf("raw PrepareQuery = %q, want %q", d2, raw)
	}
}
