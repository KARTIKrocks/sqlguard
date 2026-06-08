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
	Version   int             `yaml:"version"`
	Strict    bool            `yaml:"strict"`
	Rules     RulesConfig     `yaml:"rules"`
	SlowQuery SlowQueryConfig `yaml:"slow-query"`
	Dedup     DedupConfig     `yaml:"dedup"`
	Scan      ScanConfig      `yaml:"scan"`
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

// SlowQueryConfig configures the middleware slow-query threshold.
type SlowQueryConfig struct {
	// Threshold is a Go duration string, e.g. "200ms".
	Threshold string `yaml:"threshold"`
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

	checkName := func(name string) error {
		if !known[name] {
			return warn("unknown rule %q (known: %s)", name, strings.Join(analyzer.RuleNames(), ", "))
		}
		return nil
	}

	for _, name := range c.Rules.Disable {
		if err := checkName(name); err != nil {
			return p, err
		}
		p.Disabled[name] = true
	}
	for _, name := range c.Rules.Only {
		if err := checkName(name); err != nil {
			return p, err
		}
		p.Only[name] = true
	}
	for name, sevStr := range c.Rules.Severity {
		if err := checkName(name); err != nil {
			return p, err
		}
		sev, off, ok := parseSeverity(sevStr)
		if !ok {
			if err := warn("rule %q: invalid severity %q", name, sevStr); err != nil {
				return p, err
			}
			continue
		}
		if off {
			p.Disabled[name] = true
			continue
		}
		p.Severity[name] = sev
	}
	for name, kv := range c.Rules.Settings {
		if err := checkName(name); err != nil {
			return p, err
		}
		p.Settings[name] = analyzer.Settings(kv)
	}
	return p, nil
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

// SlowQueryThreshold returns the configured slow-query threshold. ok is false
// when unset, in which case the caller keeps its own default.
func (c *Config) SlowQueryThreshold() (d time.Duration, ok bool, err error) {
	s := strings.TrimSpace(c.SlowQuery.Threshold)
	if s == "" {
		return 0, false, nil
	}
	d, err = time.ParseDuration(s)
	if err != nil {
		return 0, false, fmt.Errorf("sqlguard config: slow-query.threshold %q: %w", s, err)
	}
	return d, true, nil
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
