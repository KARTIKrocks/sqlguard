// Package config loads and applies .sqlguard.yml configuration.
//
// It is the only package that depends on a YAML library. Importing
// sqlguard/analyzer or sqlguard/middleware does NOT pull YAML in; only code
// that opts into file-based configuration through this package does. The
// analyzer stays parser- and config-agnostic: config translates a Config
// into an analyzer.Profile, which the analyzer applies once at construction.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"gopkg.in/yaml.v3"
)

// ConfigFileNames are the file names Discover looks for, in order.
var ConfigFileNames = []string{".sqlguard.yml", ".sqlguard.yaml"}

// Config mirrors the .sqlguard.yml schema. The Version field is reserved for
// forward compatibility: older binaries reading a newer config degrade with
// warnings rather than failing, unless Strict is set.
type Config struct {
	Version int         `yaml:"version"`
	Strict  bool        `yaml:"strict"`
	Rules   RulesConfig `yaml:"rules"`
	Dedup   DedupConfig `yaml:"dedup"`
	Scan    ScanConfig  `yaml:"scan"`
	// Redact controls Result.Query literal redaction. Pointer so an unset
	// key means "use the safe default" (redact). Set `redact: false` only
	// when the query text is trusted (local debugging).
	Redact *bool `yaml:"redact"`

	warnings []string
}

// RulesConfig configures which rules run, their severity, and per-rule
// settings.
type RulesConfig struct {
	// Disable turns off the named rules.
	Disable []string `yaml:"disable"`
	// Only, when non-empty, is a whitelist: only these rules run.
	Only []string `yaml:"only"`
	// Severity overrides per rule: info | warning | critical | off
	// ("off" is equivalent to disabling the rule).
	Severity map[string]string `yaml:"severity"`
	// Settings holds per-rule tunables, e.g. leading-wildcard.min-length.
	Settings map[string]map[string]any `yaml:"settings"`
}

// DedupConfig configures runtime suppression of repeated static findings.
type DedupConfig struct {
	// Window is a Go duration string, e.g. "1m". The same finding (rule +
	// query fingerprint) is reported at most once per window. "0" disables
	// dedup (report every occurrence). Unset keeps the middleware default.
	Window string `yaml:"window"`
}

// ScanConfig holds settings that apply only to the static scanner.
type ScanConfig struct {
	// ExcludePaths is a list of regular expressions matched against scanned
	// file paths; matching files are skipped.
	ExcludePaths []string `yaml:"exclude-paths"`
}

// Default returns an empty configuration: every rule enabled at its default
// severity and settings. Used when no .sqlguard.yml is found.
func Default() *Config { return &Config{Version: 1} }

// Load reads and parses the config at path. Parsing is lenient by default so
// a config written for a newer sqlguard still loads on an older binary;
// unknown top-level keys become warnings. If the file sets `strict: true`,
// unknown keys are a hard error instead.
func Load(path string) (*Config, error) {
	//nolint:gosec // G304: reading the .sqlguard.yml the caller pointed us at
	// (via Discover or an explicit --config) is the entire purpose of Load.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sqlguard config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("sqlguard config %s: %w", path, err)
	}

	// Detect unknown fields with a second strict decode. yaml.v3 surfaces the
	// first unknown field as an error; we treat it as fatal only in strict
	// mode, otherwise as a warning so forward-compatible configs still work.
	if strictErr := strictDecode(data); strictErr != nil {
		if c.Strict {
			return nil, fmt.Errorf("sqlguard config %s (strict): %w", path, strictErr)
		}
		c.warnings = append(c.warnings, strictErr.Error())
	}
	return &c, nil
}

func strictDecode(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var probe Config
	if err := dec.Decode(&probe); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Discover walks startDir and its parents looking for a config file. It stops
// at a directory containing a .git entry (project root) after checking that
// directory, or at the filesystem root. It returns Default() and an empty
// path when no config file is found.
func Discover(startDir string) (cfg *Config, path string, err error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, "", err
	}
	for {
		for _, name := range ConfigFileNames {
			p := filepath.Join(dir, name)
			if st, statErr := os.Stat(p); statErr == nil && !st.IsDir() {
				c, loadErr := Load(p)
				return c, p, loadErr
			}
		}
		if isProjectRoot(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return Default(), "", nil
}

func isProjectRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Warnings returns non-fatal issues collected while loading or resolving the
// config (unknown keys in lenient mode, unknown rule names, bad severities).
// Callers should surface these to the user.
func (c *Config) Warnings() []string { return c.warnings }

// Profile resolves the config into an analyzer.Profile. Unknown rule names
// and unparseable severities are warnings (or errors if Strict). A severity
// of "off" disables the rule. The returned Profile is ready to pass to
// analyzer.DefaultWithProfile.
func (c *Config) Profile() (analyzer.Profile, error) {
	known := make(map[string]bool)
	for _, n := range analyzer.RuleNames() {
		known[n] = true
	}

	p := analyzer.Profile{
		Disabled: map[string]bool{},
		Only:     map[string]bool{},
		Severity: map[string]analyzer.Severity{},
		Settings: map[string]analyzer.Settings{},
		RawQuery: c.rawQuery(),
	}

	warn := func(format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		if c.Strict {
			return errors.New(msg)
		}
		c.warnings = append(c.warnings, msg)
		return nil
	}

	// checkName reports whether the name is usable. In lenient mode an unknown
	// name warns and is then *ignored* — honouring it would let one typo in
	// `only:` act as a whitelist that matches nothing, which since 0.3 turns
	// off the runtime and plan findings too, not just the static scan.
	checkName := func(name string) (bool, error) {
		if !known[name] {
			if err := warn("unknown rule %q (known: %s)", name, strings.Join(analyzer.RuleNames(), ", ")); err != nil {
				return false, err
			}
			return false, nil
		}
		return true, nil
	}

	if err := collectNames(c.Rules.Disable, p.Disabled, checkName); err != nil {
		return p, err
	}
	if err := collectNames(c.Rules.Only, p.Only, checkName); err != nil {
		return p, err
	}

	if err := applySeverities(c.Rules.Severity, &p, checkName, warn); err != nil {
		return p, err
	}
	if err := checkScanHasRules(c.Rules.Only, p, warn); err != nil {
		return p, err
	}

	for name, kv := range c.Rules.Settings {
		ok, err := checkName(name)
		if err != nil {
			return p, err
		}
		if !ok {
			continue
		}
		kept, err := checkSettings(name, kv, warn)
		if err != nil {
			return p, err
		}
		if len(kept) > 0 {
			p.Settings[name] = kept
		}
	}
	return p, nil
}

// checkScanHasRules reports an `only:` list that leaves the scanner with
// nothing to run. It asks the profile the same question the Analyzer does —
// does any evaluated rule survive Skip — rather than testing the list against
// EvaluatedRuleNames, which was blind to a name that `only:` selects and
// `disable:` (or `severity: off`) then takes away again.
//
// Three shapes reach here:
//
//   - `only: [slow-query]` — valid names now that every documented rule is
//     addressable, but none of them runs over a statement.
//   - `only: [select-star]` with `disable: [select-star]` — the whitelist
//     excludes everything else and the disabled set removes the remainder.
//   - `only: [selct-star]` — every name unknown, so the whitelist resolves to
//     empty; an empty whitelist is not a whitelist, and *every* rule runs,
//     which is the opposite of the narrowing that was asked for.
//
// All three report nothing on any codebase, which reads as a clean scan.
//
// The check is gated on `only:` being configured. Disabling every rule without
// one is a deliberate act — using sqlguard purely for its runtime findings is
// a legitimate setup — and does not deserve a warning.
func checkScanHasRules(configuredOnly []string, p analyzer.Profile, warn func(string, ...any) error) error {
	if len(configuredOnly) == 0 {
		return nil
	}
	if len(p.Only) == 0 {
		return warn("rules.only named no rule that exists, so it selects nothing and every rule runs")
	}
	for _, name := range analyzer.EvaluatedRuleNames() {
		if !p.Skip(name) {
			return nil
		}
	}
	return warn("rules.only leaves no rule that runs over a statement, so nothing will be scanned " +
		"(runtime and EXPLAIN rules are reported by the middleware and `sqlguard explain`, not the scanner)")
}

// collectNames adds each usable name to the set. An unknown name is reported
// by checkName and then left out: warning about a typo and acting on it
// anyway is how one bad entry in `only:` becomes a whitelist matching nothing.
func collectNames(names []string, into map[string]bool, checkName func(string) (bool, error)) error {
	for _, name := range names {
		ok, err := checkName(name)
		if err != nil {
			return err
		}
		if ok {
			into[name] = true
		}
	}
	return nil
}

// applySeverities resolves the `rules.severity` map onto the profile. A
// severity of "off" disables the rule rather than setting one, which is what
// makes `severity: {slow-query: off}` equivalent to listing it under
// `disable`.
func applySeverities(
	sevs map[string]string,
	p *analyzer.Profile,
	checkName func(string) (bool, error),
	warn func(string, ...any) error,
) error {
	for name, sevStr := range sevs {
		ok, err := checkName(name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		sev, off, valid := parseSeverity(sevStr)
		if !valid {
			if err := warn("rule %q: invalid severity %q", name, sevStr); err != nil {
				return err
			}
			continue
		}
		if off {
			p.Disabled[name] = true
			continue
		}
		p.Severity[name] = sev
	}
	return nil
}

// settingKind is how a per-rule setting is read back, so a value that will not
// survive the read can be reported here instead of silently becoming a
// default. analyzer.Settings.Duration and .Int both fall back on a bad value,
// which would otherwise turn a typo into a wrong threshold — or, for
// n-plus-one, into detection that never switches on.
type settingKind int

const (
	settingDuration settingKind = iota
	settingInt
	// settingPositiveInt and settingPositiveDuration additionally reject zero
	// and negatives, for a tunable where a non-positive value is never what
	// anyone means: it either silently switches the feature off, or — for a
	// latency threshold — matches every query and floods the reporter.
	settingPositiveInt
	settingPositiveDuration
)

// ruleSettings is the complete set of tunables, by rule and key. It is
// complete on purpose: a rule absent from this map reads no settings at all,
// so any key given for it is a mistake, and a key absent from a listed rule is
// a misspelling. Either way the value is silently ignored at read time, which
// is the failure this validation exists to prevent. Adding a tunable to a rule
// means adding it here.
var ruleSettings = map[string]map[string]settingKind{
	"slow-query":        {"threshold": settingPositiveDuration},
	"n-plus-one":        {"threshold": settingPositiveInt, "window": settingPositiveDuration},
	"leading-wildcard":  {"min-length": settingInt},
	"in-list-too-large": {"max-length": settingInt},
	"large-offset":      {"threshold": settingInt},
}

// pairedSettings names settings that only take effect together. n-plus-one
// needs both to switch detection on, so half a block is silently inert.
var pairedSettings = map[string][]string{
	"n-plus-one": {"threshold", "window"},
}

// checkSettings reports a setting key the rule does not have, a value that
// will not read back as its kind, and a half-specified pair. It returns the
// settings that survived.
//
// Returning a subset is the point: in lenient mode a warning does not stop the
// load, and carrying a rejected value through to the profile would mean the
// reader still acts on it. `slow-query.threshold: 0` warned and then matched
// every query anyway, flooding the reporter — the warning named the problem
// while the problem still happened. A value this reports is a value the rules
// must not see.
func checkSettings(rule string, kv map[string]any, warn func(string, ...any) error) (analyzer.Settings, error) {
	kinds, tunable := ruleSettings[rule]
	kept := make(analyzer.Settings, len(kv))
	for key, v := range kv {
		kind, known := kinds[key]
		if !known {
			if !tunable {
				if err := warn("rule %q has no settings, so %q is ignored", rule, key); err != nil {
					return nil, err
				}
				continue
			}
			if err := warn("rule %q: unknown setting %q (known: %s)",
				rule, key, strings.Join(sortedKeys(kinds), ", ")); err != nil {
				return nil, err
			}
			continue
		}
		bad, err := checkSettingValue(rule, key, kind, v, warn)
		if err != nil {
			return nil, err
		}
		if !bad {
			kept[key] = v
		}
	}

	pair := pairedSettings[rule]
	if len(pair) == 0 {
		return kept, nil
	}
	// Judged on what survived: a pair whose other half was rejected is just as
	// inert as one whose other half was never written.
	var have, missing []string
	for _, key := range pair {
		if _, present := kept[key]; present {
			have = append(have, key)
		} else {
			missing = append(missing, key)
		}
	}
	if len(have) > 0 && len(missing) > 0 {
		return kept, warn("rule %q: setting %q has no effect without %q",
			rule, strings.Join(have, ", "), strings.Join(missing, ", "))
	}
	return kept, nil
}

// checkSettingValue reports a value the reader cannot use. bad is true when
// the value was rejected, so the caller can keep it out of the profile.
func checkSettingValue(rule, key string, kind settingKind, v any, warn func(string, ...any) error) (bad bool, err error) {
	// Validate through the same accessors the rules read with, so a value
	// accepted here can never be one the reader quietly replaces with its
	// default. A second copy of these parsing rules is exactly how the two
	// drift apart.
	one := analyzer.Settings{key: v}

	switch kind {
	case settingDuration, settingPositiveDuration:
		d, ok := one.LookupDuration(key)
		if !ok {
			// A bool, a list or a map reads back as the default, which for
			// n-plus-one.window means detection silently never switches on.
			return true, warn("rule %q: setting %q: expected a duration or a number, got %v", rule, key, v)
		}
		if kind == settingPositiveDuration && d <= 0 {
			return true, warn("rule %q: setting %q must be greater than 0, got %v", rule, key, v)
		}
	case settingInt, settingPositiveInt:
		n, ok := one.LookupInt(key)
		if !ok {
			// A quoted number is the common YAML slip. Settings.Int does not
			// accept a string, so it would read back as the default: for
			// n-plus-one.threshold that means detection never switches on.
			return true, warn("rule %q: setting %q: expected a number, got %v (quoted numbers are strings in YAML)",
				rule, key, v)
		}
		if kind == settingPositiveInt && n <= 0 {
			return true, warn("rule %q: setting %q must be greater than 0, got %d", rule, key, n)
		}
	}
	return false, nil
}

func sortedKeys(m map[string]settingKind) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rawQuery reports whether Result.Query redaction is disabled. Redaction is
// the default (PII-safe); only an explicit `redact: false` turns it off.
func (c *Config) rawQuery() bool { return c.Redact != nil && !*c.Redact }

// Analyzer is a convenience that builds an analyzer from the config's
// Profile using the fallback parser. Callers wanting a real dialect parser
// should take the Profile and combine with analyzer.DefaultWithProfile +
// WithParser themselves.
func (c *Config) Analyzer() (*analyzer.Analyzer, error) {
	p, err := c.Profile()
	if err != nil {
		return nil, err
	}
	return analyzer.DefaultWithProfile(p), nil
}

// DedupWindow returns the configured static-finding dedup window. ok is false
// when unset, in which case the middleware keeps its own default. A configured
// "0" returns ok=true with d=0, which disables dedup (report every occurrence).
func (c *Config) DedupWindow() (d time.Duration, ok bool, err error) {
	s := strings.TrimSpace(c.Dedup.Window)
	if s == "" {
		return 0, false, nil
	}
	d, err = time.ParseDuration(s)
	if err != nil {
		return 0, false, fmt.Errorf("sqlguard config: dedup.window %q: %w", s, err)
	}
	return d, true, nil
}

// ExcludeMatcher compiles Scan.ExcludePaths into a single predicate. It
// returns a nil func (never excludes) when no patterns are configured.
func (c *Config) ExcludeMatcher() (func(path string) bool, error) {
	if len(c.Scan.ExcludePaths) == 0 {
		return nil, nil
	}
	res := make([]*regexp.Regexp, 0, len(c.Scan.ExcludePaths))
	for _, pat := range c.Scan.ExcludePaths {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("sqlguard config: scan.exclude-paths %q: %w", pat, err)
		}
		res = append(res, re)
	}
	return func(path string) bool {
		for _, re := range res {
			if re.MatchString(path) {
				return true
			}
		}
		return false
	}, nil
}

// parseSeverity maps a config severity string to an analyzer.Severity.
// "off" / "none" / "disabled" report off=true (disable the rule).
func parseSeverity(s string) (sev analyzer.Severity, off bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "info":
		return analyzer.SeverityInfo, false, true
	case "warning", "warn":
		return analyzer.SeverityWarning, false, true
	case "critical", "error":
		return analyzer.SeverityCritical, false, true
	case "off", "none", "disabled":
		return 0, true, true
	default:
		return 0, false, false
	}
}
