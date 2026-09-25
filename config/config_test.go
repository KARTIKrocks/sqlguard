package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Two warnings: the name itself, and what dropping it did to the list.
	if len(c.Warnings()) != 2 {
		t.Errorf("expected the unknown-name warning and the consequence, got %v", c.Warnings())
	}
	if !strings.Contains(strings.Join(c.Warnings(), " "), "unknown rule") {
		t.Errorf("the unknown name was not reported: %v", c.Warnings())
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

// TestProfile_OnlySelectingNothingRunnableWarns covers a shape that could not
// exist before every rule became addressable: `only: [slow-query]` is now a
// valid list that leaves the scanner with no rule to run, so `sqlguard scan`
// reports nothing on any codebase and CI goes green on a tree full of
// SELECT *. It has to say so.
func TestProfile_OnlySelectingNothingRunnableWarns(t *testing.T) {
	for _, only := range [][]string{
		{"slow-query"},
		{"seq-scan", "filesort"},
		{"slow-query", "n-plus-one", "high-cost"},
	} {
		t.Run(strings.Join(only, ","), func(t *testing.T) {
			c := &Config{Rules: RulesConfig{Only: only}}
			if _, err := c.Profile(); err != nil {
				t.Fatalf("lenient mode should not fail: %v", err)
			}
			if len(c.Warnings()) != 1 {
				t.Fatalf("expected a warning, got %v", c.Warnings())
			}
			if !strings.Contains(c.Warnings()[0], "nothing will be scanned") {
				t.Errorf("unexpected warning: %v", c.Warnings()[0])
			}
		})
	}

	// A name the whitelist selects and `disable` (or `severity: off`) then
	// takes away again leaves the scanner with nothing, but reads like a
	// perfectly ordinary narrowing config. Testing the list against the
	// registry could not see it; asking the profile whether any rule survives
	// can.
	t.Run("only and disable cancelling out", func(t *testing.T) {
		c := &Config{Rules: RulesConfig{
			Only:    []string{"select-star"},
			Disable: []string{"select-star"},
		}}
		if _, err := c.Profile(); err != nil {
			t.Fatalf("lenient mode should not fail: %v", err)
		}
		if len(c.Warnings()) != 1 || !strings.Contains(c.Warnings()[0], "nothing will be scanned") {
			t.Errorf("expected the nothing-scanned warning, got %v", c.Warnings())
		}
	})

	t.Run("only and severity off cancelling out", func(t *testing.T) {
		c := &Config{Rules: RulesConfig{
			Only:     []string{"select-star"},
			Severity: map[string]string{"select-star": "off"},
		}}
		if _, err := c.Profile(); err != nil {
			t.Fatalf("lenient mode should not fail: %v", err)
		}
		if len(c.Warnings()) != 1 || !strings.Contains(c.Warnings()[0], "nothing will be scanned") {
			t.Errorf("expected the nothing-scanned warning, got %v", c.Warnings())
		}
	})

	// Disabling everything without an `only:` list is a deliberate setup —
	// using sqlguard purely for its runtime findings — and must stay quiet.
	t.Run("disabling every rule without only stays quiet", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Disable: analyzer.EvaluatedRuleNames()}}
		if _, err := c.Profile(); err != nil {
			t.Fatalf("this is a legitimate config: %v", err)
		}
		if len(c.Warnings()) != 0 {
			t.Errorf("unexpected warning: %v", c.Warnings())
		}
	})

	t.Run("a list with one runnable rule is fine", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Only: []string{"slow-query", "select-star"}}}
		if _, err := c.Profile(); err != nil {
			t.Errorf("a mixed list should be accepted: %v", err)
		}
	})

	t.Run("no only list is fine", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Disable: []string{"slow-query"}}}
		if _, err := c.Profile(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestProfile_ValidatesSettingKeys closes the last silent typo: the rule name
// is known and the value is well-formed, but the key is misspelled, so the
// setting is simply absent and the built-in default stands.
func TestProfile_ValidatesSettingKeys(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]map[string]any
		wantIn   string
	}{
		{"misspelled key on a tunable rule",
			//nolint:misspell // the typo is the fixture: this is the case under test
			map[string]map[string]any{"slow-query": {"threshhold": "1s"}}, "unknown setting"},
		{"key on a rule with no settings",
			map[string]map[string]any{"select-star": {"threshold": 1}}, "has no settings"},
		{"non-numeric, non-string duration",
			map[string]map[string]any{"n-plus-one": {"threshold": 10, "window": true}}, "expected a duration"},
		{"zero threshold means off, silently",
			map[string]map[string]any{"n-plus-one": {"threshold": 0, "window": "1m"}}, "greater than 0"},
		{"negative threshold",
			map[string]map[string]any{"n-plus-one": {"threshold": -1, "window": "1m"}}, "greater than 0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Rules: RulesConfig{Settings: tc.settings}}
			if _, err := c.Profile(); err != nil {
				t.Fatalf("lenient mode should not fail: %v", err)
			}
			if len(c.Warnings()) == 0 {
				t.Fatal("expected a warning")
			}
			if !strings.Contains(c.Warnings()[0], tc.wantIn) {
				t.Errorf("warning %q does not mention %q", c.Warnings()[0], tc.wantIn)
			}

			strict := &Config{Strict: true, Rules: RulesConfig{Settings: tc.settings}}
			if _, err := strict.Profile(); err == nil {
				t.Error("expected strict mode to reject it")
			}
		})
	}

	t.Run("every documented key is accepted", func(t *testing.T) {
		c := &Config{Strict: true, Rules: RulesConfig{Settings: map[string]map[string]any{
			"slow-query":        {"threshold": "500ms"},
			"n-plus-one":        {"threshold": 5, "window": "2s"},
			"leading-wildcard":  {"min-length": 4},
			"in-list-too-large": {"max-length": 50},
			"large-offset":      {"threshold": 2000},
		}}}
		if _, err := c.Profile(); err != nil {
			t.Errorf("documented settings rejected: %v", err)
		}
	})
}

// TestProfile_RejectsNonPositiveDurations covers the worst shape on the
// branch. A slow-query threshold of 0 — or 0.5, which truncated to 0 before
// Settings.Duration was fixed to scale first — matches every successful query,
// so the middleware reports `slow-query` on all of them and floods the log
// sink it exists to protect.
func TestProfile_RejectsNonPositiveDurations(t *testing.T) {
	cases := []struct {
		name  string
		rule  string
		key   string
		value any
	}{
		{"zero threshold flags every query", "slow-query", "threshold", 0},
		{"zero duration string", "slow-query", "threshold", "0s"},
		{"negative", "slow-query", "threshold", "-1s"},
		{"zero window leaves N+1 off", "n-plus-one", "window", "0s"},
		{"negative window", "n-plus-one", "window", "-1m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := map[string]map[string]any{tc.rule: {tc.key: tc.value}}
			if tc.rule == "n-plus-one" {
				settings[tc.rule]["threshold"] = 5
			}

			c := &Config{Rules: RulesConfig{Settings: settings}}
			if _, err := c.Profile(); err != nil {
				t.Fatalf("lenient mode should not fail: %v", err)
			}
			if len(c.Warnings()) == 0 {
				t.Fatal("expected a warning")
			}
			if !strings.Contains(c.Warnings()[0], "greater than 0") {
				t.Errorf("unexpected warning: %v", c.Warnings()[0])
			}

			strict := &Config{Strict: true, Rules: RulesConfig{Settings: settings}}
			if _, err := strict.Profile(); err == nil {
				t.Error("expected strict mode to reject it")
			}
		})
	}

	// A sub-millisecond threshold is legitimate. It used to truncate to zero
	// — time.Duration(0.5) is 0 — which turned a tight threshold into one
	// that matched every query, the opposite of what was asked for.
	for _, tc := range []struct {
		value any
		want  time.Duration
	}{
		{1.5, 1500 * time.Microsecond},
		{0.5, 500 * time.Microsecond},
		{0.0004, 400 * time.Nanosecond},
	} {
		t.Run(fmt.Sprintf("fractional %v round trips", tc.value), func(t *testing.T) {
			c := &Config{Strict: true, Rules: RulesConfig{Settings: map[string]map[string]any{
				"slow-query": {"threshold": tc.value},
			}}}
			p, err := c.Profile()
			if err != nil {
				t.Fatalf("%v ms should be accepted: %v", tc.value, err)
			}
			if d := p.Settings["slow-query"].Duration("threshold", 0); d != tc.want {
				t.Errorf("read back %v, want %v — the float conversion truncated", d, tc.want)
			}
		})
	}
}

// TestProfile_OnlyResolvingToNothingWarns covers the inverse of the
// selects-nothing case: when every name is unknown the whitelist resolves to
// empty, and an empty whitelist is not a whitelist — every rule runs, which is
// the opposite of what the user asked for.
func TestProfile_OnlyResolvingToNothingWarns(t *testing.T) {
	c := &Config{Rules: RulesConfig{Only: []string{"selct-star"}}}

	p, err := c.Profile()
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if len(p.Only) != 0 {
		t.Fatalf("expected an empty whitelist, got %v", p.Only)
	}

	var found bool
	for _, w := range c.Warnings() {
		if strings.Contains(w, "selects nothing and every rule runs") {
			found = true
		}
	}
	if !found {
		t.Errorf("the consequence was not reported: %v", c.Warnings())
	}
}
