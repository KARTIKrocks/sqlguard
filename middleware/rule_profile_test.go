package middleware

import (
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// snapshot returns a copy of what the reporter has been handed so far.
func (c *countingReporter) snapshot() []analyzer.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]analyzer.Result(nil), c.results...)
}

// The runtime findings are registered rules that this package builds itself.
// These tests pin that `disable`, `severity` and `settings` reach them, which
// is what makes the config surface uniform across all three entry points.

func profileGuard(t *testing.T, p analyzer.Profile, rep *countingReporter, extra ...Option) *Guard {
	t.Helper()
	opts := append([]Option{
		WithAnalyzer(analyzer.DefaultWithProfile(p)),
		WithReporter(rep),
	}, extra...)
	return NewGuard(opts...)
}

func TestSlowQuery_DisabledByProfile(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{Disabled: map[string]bool{"slow-query": true}}, rep)

	g.CheckLatency("SELECT id FROM t WHERE id = ?", time.Second)

	if got := rep.snapshot(); len(got) != 0 {
		t.Errorf("disabled slow-query still reported: %+v", got)
	}
}

func TestSlowQuery_EnabledByDefault(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{}, rep)

	g.CheckLatency("SELECT id FROM t WHERE id = ?", time.Second)

	got := rep.snapshot()
	if len(got) != 1 || got[0].RuleName != "slow-query" {
		t.Fatalf("expected one slow-query finding, got %+v", got)
	}
	if got[0].Severity != analyzer.SeverityWarning {
		t.Errorf("severity = %v, want WARNING", got[0].Severity)
	}
}

func TestSlowQuery_SeverityOverride(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Severity: map[string]analyzer.Severity{"slow-query": analyzer.SeverityCritical},
	}, rep)

	g.CheckLatency("SELECT id FROM t WHERE id = ?", time.Second)

	got := rep.snapshot()
	if len(got) != 1 || got[0].Severity != analyzer.SeverityCritical {
		t.Errorf("expected a CRITICAL slow-query, got %+v", got)
	}
}

func TestSlowQuery_ThresholdFromSettings(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Settings: map[string]analyzer.Settings{"slow-query": {"threshold": "500ms"}},
	}, rep)

	g.CheckLatency("SELECT 1", 300*time.Millisecond) // under the configured 500ms
	if got := rep.snapshot(); len(got) != 0 {
		t.Fatalf("300ms should be under a 500ms threshold, got %+v", got)
	}

	g.CheckLatency("SELECT 1", 600*time.Millisecond)
	if got := rep.snapshot(); len(got) != 1 {
		t.Errorf("600ms should exceed a 500ms threshold, got %+v", got)
	}
}

// TestSlowQuery_ExplicitOptionBeatsSettings pins the precedence: a Go option
// names the threshold deliberately, so file configuration does not move it.
func TestSlowQuery_ExplicitOptionBeatsSettings(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Settings: map[string]analyzer.Settings{"slow-query": {"threshold": "500ms"}},
	}, rep, WithSlowQueryThreshold(50*time.Millisecond))

	g.CheckLatency("SELECT 1", 100*time.Millisecond)

	if got := rep.snapshot(); len(got) != 1 {
		t.Errorf("the explicit 50ms option should have won over the configured 500ms, got %+v", got)
	}
}

func TestNPlusOne_DisabledByProfile(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{Disabled: map[string]bool{"n-plus-one": true}}, rep,
		WithN1Detection(2, time.Minute))

	if g.tracker != nil {
		t.Fatal("a disabled n-plus-one should not build a tracker")
	}
	for range 5 {
		g.Check("SELECT id FROM t WHERE id = ?")
	}
	for _, r := range rep.snapshot() {
		if r.RuleName == "n-plus-one" {
			t.Errorf("disabled n-plus-one still reported: %+v", r)
		}
	}
}

func TestNPlusOne_SeverityOverride(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Severity: map[string]analyzer.Severity{"n-plus-one": analyzer.SeverityCritical},
	}, rep, WithN1Detection(2, time.Minute))

	for range 3 {
		g.Check("SELECT id FROM t WHERE id = ? LIMIT 1")
	}

	var found bool
	for _, r := range rep.snapshot() {
		if r.RuleName == "n-plus-one" {
			found = true
			if r.Severity != analyzer.SeverityCritical {
				t.Errorf("n-plus-one severity = %v, want CRITICAL", r.Severity)
			}
		}
	}
	if !found {
		t.Error("expected an n-plus-one finding")
	}
}

// TestNPlusOne_EnabledBySettings covers the only way a config file can turn
// N+1 on: before this, detection was reachable only from Go via
// WithN1Detection, so .sqlguard.yml could not enable it at all.
func TestNPlusOne_EnabledBySettings(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Settings: map[string]analyzer.Settings{
			"n-plus-one": {"threshold": 2, "window": "1m"},
		},
	}, rep)

	if g.tracker == nil {
		t.Fatal("settings should have enabled the tracker")
	}
	for range 3 {
		g.Check("SELECT id FROM t WHERE id = ? LIMIT 1")
	}

	var found bool
	for _, r := range rep.snapshot() {
		if r.RuleName == "n-plus-one" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an n-plus-one finding, got %+v", rep.snapshot())
	}
}

// TestDisableBeatsExplicitGoOption pins the precedence documented under
// Configuration → Precedence, and the asymmetry in it: a Go option wins for a
// *threshold*, but `disable` is an instruction and wins over the Go call, so
// an operator can silence a noisy rule by editing .sqlguard.yml without a
// redeploy.
func TestDisableBeatsExplicitGoOption(t *testing.T) {
	t.Run("slow-query", func(t *testing.T) {
		rep := &countingReporter{}
		g := profileGuard(t, analyzer.Profile{Disabled: map[string]bool{"slow-query": true}}, rep,
			WithSlowQueryThreshold(time.Millisecond))

		g.CheckLatency("SELECT 1", time.Second)

		if got := rep.snapshot(); len(got) != 0 {
			t.Errorf("disable should outrank WithSlowQueryThreshold, got %+v", got)
		}
	})

	t.Run("n-plus-one", func(t *testing.T) {
		rep := &countingReporter{}
		g := profileGuard(t, analyzer.Profile{Disabled: map[string]bool{"n-plus-one": true}}, rep,
			WithN1Detection(2, time.Minute))

		if g.tracker != nil {
			t.Error("disable should outrank WithN1Detection")
		}
	})

	t.Run("an only list does not reach the runtime findings", func(t *testing.T) {
		rep := &countingReporter{}
		g := profileGuard(t, analyzer.Profile{Only: map[string]bool{"select-star": true}}, rep,
			WithSlowQueryThreshold(time.Millisecond), WithN1Detection(2, time.Minute))

		g.CheckLatency("SELECT 1", time.Second)

		// `only:` selects which rules run over a statement. slow-query and
		// n-plus-one are not evaluated over one, and a list written to focus
		// `sqlguard scan` should not switch off a running app's latency and
		// N+1 reporting without saying so.
		if got := rep.snapshot(); len(got) != 1 || got[0].RuleName != "slow-query" {
			t.Errorf("an `only` whitelist should not silence slow-query, got %+v", got)
		}
		if g.tracker == nil {
			t.Error("an `only` whitelist should not stop the N+1 tracker being built")
		}
	})
}

// A sub-millisecond threshold must behave as written, not as zero.
func TestSlowQuery_FractionalThreshold(t *testing.T) {
	rep := &countingReporter{}
	g := profileGuard(t, analyzer.Profile{
		Settings: map[string]analyzer.Settings{"slow-query": {"threshold": 0.5}},
	}, rep)

	g.CheckLatency("SELECT 1", 100*time.Microsecond) // under 500µs
	if got := rep.snapshot(); len(got) != 0 {
		t.Fatalf("100µs is under a 500µs threshold; a 0 threshold would flag it: %+v", got)
	}

	g.CheckLatency("SELECT 1", 900*time.Microsecond) // over 500µs
	if got := rep.snapshot(); len(got) != 1 {
		t.Errorf("900µs should exceed a 500µs threshold, got %+v", got)
	}
}
