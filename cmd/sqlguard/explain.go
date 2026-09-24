package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/KARTIKrocks/sqlguard/explain"
	"github.com/spf13/cobra"
)

var (
	explainDSN      string
	explainDialect  string
	explainFormat   string
	explainAllowDML bool
)

var explainCmd = &cobra.Command{
	Use:   `explain "SQL QUERY"`,
	Short: "Run EXPLAIN on a query against a live database",
	Long:  "Connects to a database and runs EXPLAIN to detect performance issues like sequential scans and missing indexes.",
	Args:  cobra.ExactArgs(1),
	RunE:  runExplain,
}

func init() {
	explainCmd.Flags().StringVar(&explainDSN, "db", "", "Database connection string (required)")
	explainCmd.Flags().StringVar(&explainDialect, "dialect", "postgres", "Database dialect: postgres or mysql")
	explainCmd.Flags().StringVar(&explainFormat, "format", "console", "Output format: console or json")
	explainCmd.Flags().BoolVar(&explainAllowDML, "allow-dml", false, "Allow EXPLAIN on INSERT/UPDATE/DELETE (still run in an always-rolled-back transaction); refused by default")
	_ = explainCmd.MarkFlagRequired("db")
}

func runExplain(cmd *cobra.Command, args []string) error {
	// Args are valid past this point; don't dump usage for runtime errors or
	// the errIssuesFound sentinel. (Arg-parse errors still show usage.)
	cmd.SilenceUsage = true

	query := args[0]

	// Validate the format before dialing: a typo should cost an error message,
	// not a connection attempt followed by a silent fall back to console.
	rep, err := newReporter(explainFormat)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := openDB(ctx, explainDialect, explainDSN)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	var explainOpts []explain.Option
	if explainAllowDML {
		explainOpts = append(explainOpts, explain.WithAllowDML())
	}
	analyzer, err := explain.New(db, explainDialect, explainOpts...)
	if err != nil {
		return err
	}

	result, err := analyzer.Analyze(ctx, query)
	if err != nil {
		return err
	}

	if explainFormat == "json" {
		rep.Report(result.Issues)
		if werr := jsonWriteErr(); werr != nil {
			return werr
		}
		if len(result.Issues) > 0 {
			return errIssuesFound
		}
		return nil
	}

	if len(result.Issues) > 0 {
		rep.Report(result.Issues)
		fmt.Fprintf(os.Stderr, "\n%d issue(s) found in query plan\n", len(result.Issues))
		return errIssuesFound
	}

	fmt.Fprintln(os.Stderr, "No issues found in query plan")
	return nil
}
