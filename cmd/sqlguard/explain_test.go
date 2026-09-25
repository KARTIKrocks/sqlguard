package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestExplain_RejectsUnknownFormat pins the validation `explain` was missing.
// It used to accept any --format value and fall back to console, so
// `explain --format jsonn … > plan.json` dialed the database, rendered to
// stderr and left an empty file — the same failure this branch fixes for the
// spelled-correctly case. The error must also arrive before the connection
// attempt, so a typo costs a message rather than a 30-second dial.
func TestExplain_RejectsUnknownFormat(t *testing.T) {
	oldFormat, oldDSN := explainFormat, explainDSN
	explainFormat = "jsonn"
	explainDSN = "postgres://nobody@127.0.0.1:1/none"
	t.Cleanup(func() { explainFormat, explainDSN = oldFormat, oldDSN })

	err := runExplain(&cobra.Command{}, []string{"SELECT 1"})

	if err == nil {
		t.Fatal("expected an error for an unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected an unknown-format error, got %v", err)
	}
	// If this says "failed to connect", validation ran too late.
	if strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("format was validated after dialing the database: %v", err)
	}
}

// TestExplain_AcceptsKnownFormats guards the other direction: the valid
// spellings must pass validation and reach the connection attempt.
func TestExplain_AcceptsKnownFormats(t *testing.T) {
	oldFormat, oldDSN := explainFormat, explainDSN
	explainDSN = "postgres://nobody@127.0.0.1:1/none"
	t.Cleanup(func() { explainFormat, explainDSN = oldFormat, oldDSN })

	for _, f := range []string{"console", "json", ""} {
		explainFormat = f
		err := runExplain(&cobra.Command{}, []string{"SELECT 1"})
		if err == nil {
			t.Errorf("format %q: expected a connection failure, got nil", f)
			continue
		}
		if strings.Contains(err.Error(), "unknown format") {
			t.Errorf("format %q was rejected as unknown: %v", f, err)
		}
	}
}

// TestExplain_ConfigErrorsBeforeDialing pins the ordering. Config resolution
// sat after openDB, so a rule-name typo under `strict: true` cost the full
// 30-second connect timeout before surfacing — and printed its warnings
// underneath the connection noise.
func TestExplain_ConfigErrorsBeforeDialing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".sqlguard.yml")
	if err := os.WriteFile(cfgPath, []byte("strict: true\nrules:\n  disable: [no-such-rule]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldFormat, oldDSN, oldCfg := explainFormat, explainDSN, configPathFlag
	explainFormat = "console"
	// An unroutable address: reaching the dial at all would block for the
	// timeout, so a prompt return is itself part of the assertion.
	explainDSN = "postgres://nobody@192.0.2.1:5432/none"
	configPathFlag = cfgPath
	t.Cleanup(func() { explainFormat, explainDSN, configPathFlag = oldFormat, oldDSN, oldCfg })

	start := time.Now()
	err := runExplain(&cobra.Command{}, []string{"SELECT 1"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the strict config to fail the run")
	}
	if !strings.Contains(err.Error(), "no-such-rule") {
		t.Errorf("expected the config error, got %v", err)
	}
	if strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("config was resolved after dialing: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v; the run reached the dial before failing on config", elapsed)
	}
}
