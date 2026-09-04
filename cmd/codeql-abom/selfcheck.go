package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/sarifin"
)

// selfLanguage is fixed: this is a smoke test of ABOM's own database-creation path against
// GitHub Actions workflows, not a general-purpose analysis entry point.
const selfLanguage = "actions"

// runSelfcheck builds a CodeQL database from a source tree and analyzes it with a suite, as a
// smoke test of the exact path every upstream expansion depends on.
//
// It exits non-zero only when database creation or analysis itself fails. Findings are printed,
// never treated as failure: this job ships no SARIF upload and has read-only permissions, so
// there is nothing for a nonzero exit to gate.
func runSelfcheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("selfcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	suite := fs.String("suite", runner.DefaultSuite, "CodeQL suite to run")
	sourceRoot := fs.String("source-root", ".", "source tree to analyze")
	codeqlPath := fs.String("codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	workDir := fs.String("work-dir", "", "scratch directory for the database (default: a temp dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir, cleanup, err := workingDirectory(*workDir, "abom-selfcheck-*")
	if err != nil {
		return err
	}
	defer cleanup()

	dbDir := filepath.Join(dir, "db")
	sarifPath := filepath.Join(dir, "findings.sarif")

	r := &runner.Runner{CodeQLPath: *codeqlPath}
	if err := r.CreateDatabase(ctx, *sourceRoot, dbDir, selfLanguage); err != nil {
		return fmt.Errorf("selfcheck: creating a database: %w", err)
	}
	if err := r.AnalyzeDatabase(ctx, dbDir, *suite, sarifPath); err != nil {
		return fmt.Errorf("selfcheck: analyzing: %w", err)
	}

	f, err := os.Open(sarifPath)
	if err != nil {
		return fmt.Errorf("selfcheck: opening results: %w", err)
	}
	defer f.Close()

	summary, err := summarizeSARIF(f)
	if err != nil {
		return fmt.Errorf("selfcheck: reading results: %w", err)
	}
	writeSelfcheckSummary(stdout, summary)
	return nil
}

// selfcheckSummary is a pure aggregation of one SARIF document, kept separate from any I/O so
// it can be tested against a fixture instead of a real CodeQL run.
type selfcheckSummary struct {
	Rules      int
	Results    int
	BySeverity map[string]int
}

// summarizeSARIF counts rules and findings by level, streaming rather than buffering: the same
// reasoning sarifin.Stream itself documents applies here even for one repository, since a
// broad security-and-quality suite can still produce a large document.
func summarizeSARIF(r io.Reader) (selfcheckSummary, error) {
	summary := selfcheckSummary{BySeverity: map[string]int{}}
	seenRules := map[string]bool{}

	err := sarifin.Stream(r, sarifin.Handler{
		OnRule: func(rule sarifin.Rule) error {
			seenRules[rule.ID] = true
			return nil
		},
		OnResult: func(res sarifin.Result) error {
			summary.Results++
			level := res.Level
			if level == "" {
				level = "none"
			}
			summary.BySeverity[level]++
			return nil
		},
	})
	summary.Rules = len(seenRules)
	if err != nil {
		return summary, err
	}
	return summary, nil
}

func writeSelfcheckSummary(w io.Writer, s selfcheckSummary) {
	fmt.Fprintf(w, "selfcheck: %d rules evaluated, %d findings\n", s.Rules, s.Results)
	if len(s.BySeverity) == 0 {
		return
	}
	levels := make([]string, 0, len(s.BySeverity))
	for level := range s.BySeverity {
		levels = append(levels, level)
	}
	sort.Strings(levels)
	fmt.Fprint(w, "  by level:")
	for _, level := range levels {
		fmt.Fprintf(w, " %s=%d", level, s.BySeverity[level])
	}
	fmt.Fprintln(w)
}
