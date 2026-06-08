package main

import (
	"fmt"
	"os"

	"github.com/KARTIKrocks/sqlguard/config"
	"github.com/spf13/cobra"
)

var (
	configPathFlag string
	noConfigFlag   bool
)

var rootCmd = &cobra.Command{
	Use:   "sqlguard",
	Short: "Production-safe SQL query analyzer for Go applications",
	Long:  "sqlguard detects slow queries, dangerous SQL patterns, and performance issues in Go applications.",
	// main() owns error printing and exit codes. Without this, cobra prints
	// "Error: issues found" for the errIssuesFound sentinel, which is a normal
	// outcome (issues were already reported), not a CLI error.
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPathFlag, "config", "", "path to .sqlguard.yml (default: auto-discover)")
	rootCmd.PersistentFlags().BoolVar(&noConfigFlag, "no-config", false, "ignore any .sqlguard.yml and use built-in defaults")
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(explainCmd)
}

// resolveConfig loads configuration honoring --config / --no-config, falling
// back to discovery from startDir. Warnings are printed to stderr; a load
// error is returned to abort the command.
func resolveConfig(startDir string) (*config.Config, error) {
	switch {
	case noConfigFlag:
		return config.Default(), nil
	case configPathFlag != "":
		return config.Load(configPathFlag)
	default:
		c, path, err := config.Discover(startDir)
		if err != nil {
			return nil, err
		}
		if path != "" {
			_, _ = fmt.Fprintf(os.Stderr, "Using config %s\n", path)
		}
		return c, nil
	}
}

func printConfigWarnings(c *config.Config) {
	for _, w := range c.Warnings() {
		_, _ = fmt.Fprintf(os.Stderr, "sqlguard: config warning: %s\n", w)
	}
}
