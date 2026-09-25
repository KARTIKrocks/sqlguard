package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, ".sqlguard.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestLoadAndProfile(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, `
version: 1
rules:
  disable: [orderby-without-limit]
  severity:
    select-star: info
    select-without-limit: "off"
  settings:
    leading-wildcard:
      min-length: 4
    slow-query:
      threshold: 350ms
`)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	prof, err := c.Profile()
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if !prof.Disabled["orderby-without-limit"] {
		t.Error("orderby-without-limit should be disabled")
	}
	if !prof.Disabled["select-without-limit"] {
		t.Error(`severity "off" should disable select-without-limit`)
	}
	if prof.Severity["select-star"] != analyzer.SeverityInfo {
		t.Errorf("select-star severity = %v, want INFO", prof.Severity["select-star"])
	}
	if prof.Settings["leading-wildcard"].Int("min-length", 0) != 4 {
		t.Error("min-length setting not carried into profile")
	}

	if d := prof.Settings["slow-query"].Duration("threshold", 0); d != 350*time.Millisecond {
		t.Errorf("slow-query threshold = %v, want 350ms", d)
	}

	// End-to-end: the built analyzer respects the profile.
	a := analyzer.DefaultWithProfile(prof)
	got := a.Analyze("SELECT * FROM users")
	if len(got) != 1 || got[0].RuleName != "select-star" || got[0].Severity != analyzer.SeverityInfo {
		t.Errorf("expected single INFO select-star, got %+v", got)
	}
}

func TestDedupWindow(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		c := &Config{Dedup: DedupConfig{Window: "30s"}}
		d, ok, err := c.DedupWindow()
		if err != nil || !ok || d != 30*time.Second {
			t.Errorf("DedupWindow = %v, %v, %v; want 30s,true,nil", d, ok, err)
		}
	})
	t.Run("unset keeps default", func(t *testing.T) {
		c := &Config{}
		if d, ok, err := c.DedupWindow(); err != nil || ok || d != 0 {
			t.Errorf("DedupWindow = %v, %v, %v; want 0,false,nil", d, ok, err)
		}
	})
	t.Run("zero disables", func(t *testing.T) {
		c := &Config{Dedup: DedupConfig{Window: "0"}}
		if d, ok, err := c.DedupWindow(); err != nil || !ok || d != 0 {
			t.Errorf("DedupWindow = %v, %v, %v; want 0,true,nil (explicit disable)", d, ok, err)
		}
	})
	t.Run("invalid errors", func(t *testing.T) {
		c := &Config{Dedup: DedupConfig{Window: "soon"}}
		if _, _, err := c.DedupWindow(); err == nil {
			t.Error("expected error for invalid dedup.window")
		}
	})
}

func TestUnknownRuleLenientVsStrict(t *testing.T) {
	dir := t.TempDir()
	body := "rules:\n  disable: [no-such-rule]\n"

	c, err := Load(writeConfig(t, dir, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Profile(); err != nil {
		t.Fatalf("lenient Profile should not error: %v", err)
	}
	if len(c.Warnings()) == 0 {
		t.Error("expected a warning for unknown rule in lenient mode")
	}

	strict := &Config{Strict: true, Rules: RulesConfig{Disable: []string{"no-such-rule"}}}
	if _, err := strict.Profile(); err == nil {
		t.Error("expected error for unknown rule in strict mode")
	}
}

func TestUnknownKeyLenientWarnsStrictFails(t *testing.T) {
	dir := t.TempDir()

	c, err := Load(writeConfig(t, dir, "bananas: true\n"))
	if err != nil {
		t.Fatalf("lenient load should succeed: %v", err)
	}
	if len(c.Warnings()) == 0 {
		t.Error("expected warning for unknown top-level key")
	}

	if _, err := Load(writeConfig(t, dir, "strict: true\nbananas: true\n")); err == nil {
		t.Error("expected strict load to fail on unknown key")
	}
}

func TestDiscoverWalksUpAndStopsAtGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, "rules:\n  disable: [select-star]\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	c, path, err := Discover(deep)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if path == "" {
		t.Fatal("expected to find config by walking up")
	}
	prof, _ := c.Profile()
	if !prof.Disabled["select-star"] {
		t.Error("discovered config not applied")
	}
}

func TestDiscoverNoConfigReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	// .git marks the boundary so Discover does not escape the temp dir.
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)

	c, path, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if path != "" {
		t.Errorf("expected no config path, got %q", path)
	}
	if _, err := c.Profile(); err != nil {
		t.Errorf("default profile should be valid: %v", err)
	}
}

func TestExcludeMatcher(t *testing.T) {
	c := &Config{Scan: ScanConfig{ExcludePaths: []string{`(^|/)legacy/`, `_gen\.go$`}}}
	m, err := c.ExcludeMatcher()
	if err != nil {
		t.Fatalf("ExcludeMatcher: %v", err)
	}
	if !m("pkg/legacy/old.go") || !m("api/types_gen.go") {
		t.Error("expected matches for excluded paths")
	}
	if m("pkg/service/user.go") {
		t.Error("did not expect match for normal path")
	}

	none, err := (&Config{}).ExcludeMatcher()
	if err != nil {
		t.Errorf("no patterns should not error: %v", err)
	}
	if none != nil {
		t.Error("no patterns should yield a nil matcher")
	}
}

// TestProfile_AcceptsNonEvaluatedRules pins the bug this fixes: `slow-query`,
// `n-plus-one` and the five plan rules are documented in the same reference
// table as the statement rules, but were not registered, so naming any of
// them warned in lenient mode and failed outright under `strict: true`.
func TestProfile_AcceptsNonEvaluatedRules(t *testing.T) {
	names := []string{
		"slow-query", "n-plus-one",
		"seq-scan", "high-cost", "full-table-scan", "no-index-used", "filesort",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			c := &Config{
				Strict: true,
				Rules: RulesConfig{
					Disable:  []string{name},
					Severity: map[string]string{name: "critical"},
				},
			}
			p, err := c.Profile()
			if err != nil {
				t.Fatalf("strict config naming %q failed: %v", name, err)
			}
			if !p.Disabled[name] {
				t.Errorf("%q not carried into Profile.Disabled", name)
			}
			if len(c.Warnings()) != 0 {
				t.Errorf("unexpected warnings: %v", c.Warnings())
			}
		})
	}
}

// TestProfile_ValidatesSettings guards the trap that moving tunables into
// settings creates: analyzer.Settings.Duration and .Int both fall back to the
// caller's default on a value they cannot read, so an unchecked typo becomes a
// silently wrong threshold — or, for n-plus-one, detection that never switches
// on at all.
func TestProfile_ValidatesSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]map[string]any
		warnings int
	}{
		{"unparseable duration", map[string]map[string]any{
			"slow-query": {"threshold": "200mss"}}, 1},
		{"quoted number reads back as the default", map[string]map[string]any{
			"n-plus-one": {"threshold": "10", "window": "1m"}}, 1},
		{"half a paired block is inert", map[string]map[string]any{
			"n-plus-one": {"window": "1m"}}, 1},
		{"quoted int on a statement rule", map[string]map[string]any{
			"leading-wildcard": {"min-length": "4"}}, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name+" (lenient warns)", func(t *testing.T) {
			c := &Config{Rules: RulesConfig{Settings: tc.settings}}
			if _, err := c.Profile(); err != nil {
				t.Fatalf("lenient mode should not fail: %v", err)
			}
			if len(c.Warnings()) != tc.warnings {
				t.Errorf("expected %d warning(s), got %v", tc.warnings, c.Warnings())
			}
		})
		t.Run(tc.name+" (strict fails)", func(t *testing.T) {
			c := &Config{Strict: true, Rules: RulesConfig{Settings: tc.settings}}
			if _, err := c.Profile(); err == nil {
				t.Fatal("expected strict mode to reject it")
			}
		})
	}

	t.Run("valid settings pass", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Settings: map[string]map[string]any{
			"slow-query": {"threshold": "1s"},
			"n-plus-one": {"window": 500, "threshold": 10},
		}}}
		if _, err := c.Profile(); err != nil {
			t.Fatalf("valid settings rejected: %v", err)
		}
	})

	// Surrounding whitespace must not pass validation and then fall back at
	// read time: the check and the read have to agree on the same value.
	t.Run("padded duration survives the round trip", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Settings: map[string]map[string]any{
			"slow-query": {"threshold": " 500ms "},
		}}}
		p, err := c.Profile()
		if err != nil {
			t.Fatalf("a padded duration should be accepted: %v", err)
		}
		if d := p.Settings["slow-query"].Duration("threshold", time.Second); d != 500*time.Millisecond {
			t.Errorf("read back %v, want 500ms — the check and the read disagree", d)
		}
	})
}

// TestProfile_UnknownNameIsIgnoredNotHonoured pins the blast radius of a typo.
// A warned-about name must not take effect: an unknown entry in `only:` would
// otherwise act as a whitelist matching nothing, which since every rule became
// addressable also silences the runtime and plan findings.
func TestProfile_UnknownNameIsIgnoredNotHonoured(t *testing.T) {
	c := &Config{Rules: RulesConfig{Only: []string{"slect-star"}}}

	p, err := c.Profile()
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if len(c.Warnings()) != 1 {
		t.Errorf("expected one warning, got %v", c.Warnings())
	}
	if len(p.Only) != 0 {
		t.Errorf("an unknown name entered the whitelist: %v", p.Only)
	}

	a := analyzer.DefaultWithProfile(p)
	if len(a.Analyze("SELECT * FROM t")) == 0 {
		t.Error("a typo in only: silenced the static rules")
	}
	for _, name := range []string{"slow-query", "n-plus-one", "seq-scan"} {
		if !a.RuleEnabled(name) {
			t.Errorf("a typo in only: silenced %q", name)
		}
	}
}
