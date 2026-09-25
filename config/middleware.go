package config

import (
	"github.com/KARTIKrocks/sqlguard/middleware"
)

// MiddlewareOptions translates this config into middleware options: an
// analyzer built from the rule Profile, which carries the rule settings
// (including `slow-query.threshold` and the `n-plus-one` tunables), plus the
// dedup window. Combine with other middleware options as needed, e.g.:
//
//	opts, _ := cfg.MiddlewareOptions()
//	opts = append(opts, middleware.WithParser(pgparser.New()))
//	sqlguard.Register("sqlguard-pg", "pgx", opts...)
//
// Keeping this in the config package (not middleware) keeps YAML out of the
// middleware import graph for users who do not use file configuration.
func (c *Config) MiddlewareOptions() ([]middleware.Option, error) {
	a, err := c.Analyzer()
	if err != nil {
		return nil, err
	}
	// The slow-query threshold and the N+1 threshold/window travel inside the
	// analyzer's profile as rule settings, so they need no option of their
	// own here — NewGuard reads them unless a Go option names them
	// explicitly.
	opts := []middleware.Option{middleware.WithAnalyzer(a)}

	dw, ok, err := c.DedupWindow()
	if err != nil {
		return nil, err
	}
	if ok {
		opts = append(opts, middleware.WithFindingDedup(dw))
	}
	return opts, nil
}

// Middleware loads configuration and returns ready-to-use middleware
// options. If path is non-empty it is loaded directly; otherwise config is
// discovered by walking up from startDir (use "." for the working
// directory). A missing config is not an error — it yields options
// equivalent to the built-in defaults.
func Middleware(path, startDir string) ([]middleware.Option, error) {
	var (
		c   *Config
		err error
	)
	if path != "" {
		c, err = Load(path)
	} else {
		c, _, err = Discover(startDir)
	}
	if err != nil {
		return nil, err
	}
	return c.MiddlewareOptions()
}
