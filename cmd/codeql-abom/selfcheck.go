package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/sarifin"
)

// defaultSelfLanguage is what `make selfcheck` and the self-audit workflow analyze when they name
// no language: ABOM's own workflows, through the same database-creation path every expansion uses.
const defaultSelfLanguage = "actions"

// reportOnly disables the gate, for taking a first measurement of a language nothing has analyzed
// before. It is not a threshold: it is the absence of one.
const reportOnly = -1

// selfcheckConfig is one run's decisions, parsed apart from any I/O so the rules between them can
// be tested without a CodeQL CLI.
type selfcheckConfig struct {
	language   string
	suite      string
	sourceRoot string
	codeqlPath string
	workDir    string
	// maxFindings is the most this run tolerates. Zero by default, because a threshold set where
	// the code happens to be is one that moves whenever the build goes red.
	maxFindings int
}

// parseSelfcheck reads the flags and enforces the one rule between them.
//
// A language and a suite are a single decision. `runner.DefaultSuite` names the Actions findings
// suite, so a language given on its own would build, say, a Go database and run Actions queries
// against it — which finds nothing and is indistinguishable from a clean result.
func parseSelfcheck(args []string, stderr io.Writer) (selfcheckConfig, error) {
	fs := flag.NewFlagSet("selfcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", defaultSelfLanguage, "language to extract and analyze")
	suite := fs.String("suite", runner.DefaultSuite, "CodeQL suite to run")
	sourceRoot := fs.String("source-root", ".", "source tree to analyze")
	codeqlPath := fs.String("codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	workDir := fs.String("work-dir", "", "scratch directory for the database (default: a temp dir)")
	maxFindings := fs.Int("max-findings", 0,
		"fail above this many findings; -1 reports without failing")
	if err := fs.Parse(args); err != nil {
		return selfcheckConfig{}, err
	}

	suiteNamed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "suite" {
			suiteNamed = true
		}
	})
	if *language != defaultSelfLanguage && !suiteNamed {
		return selfcheckConfig{}, fmt.Errorf(
			"selfcheck: --language=%s needs its own --suite; the default suite analyzes %s",
			*language, defaultSelfLanguage)
	}

	return selfcheckConfig{
		language:    *language,
		suite:       *suite,
		sourceRoot:  *sourceRoot,
		codeqlPath:  *codeqlPath,
		workDir:     *workDir,
		maxFindings: *maxFindings,
	}, nil
}

// selfcheckVerdict decides whether a run's findings fail it, naming what fired.
//
// The rules are in the error because the alternative is an operator who knows the build is red
// and has to run CodeQL again themselves to learn why.
func selfcheckVerdict(s selfcheckSummary, maxFindings int) error {
	if maxFindings == reportOnly || s.Results <= maxFindings {
		return nil
	}
	ids := make([]string, 0, len(s.ByRule))
	for id := range s.ByRule {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	named := make([]string, 0, len(ids))
	for _, id := range ids {
		named = append(named, fmt.Sprintf("%s (%d)", id, s.ByRule[id]))
	}
	return fmt.Errorf("selfcheck: %d findings, above the %d allowed: %s",
		s.Results, maxFindings, strings.Join(named, ", "))
}

// runSelfcheck builds a CodeQL database from a source tree and analyzes it with a suite, as a
// smoke test of the exact path every upstream expansion depends on.
//
// It fails when database creation or analysis fails, and when the findings exceed --max-findings,
// which defaults to zero. Findings were report-only until 2026-09-10, when the count reached zero
// in all four languages and there was finally something for a nonzero exit to protect.
func runSelfcheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseSelfcheck(args, stderr)
	if err != nil {
		return err
	}

	dir, cleanup, err := workingDirectory(cfg.workDir, "abom-selfcheck-*")
	if err != nil {
		return err
	}
	defer cleanup()

	dbDir := filepath.Join(dir, "db")
	sarifPath := filepath.Join(dir, "findings.sarif")

	// Vendored minified libraries need no exclusion: measured 2026-09-10, the JavaScript
	// extractor skips *.min.js of its own accord, so 1.3 MB of vendored code never enters.
	r := &runner.Runner{
		CodeQLPath: cfg.codeqlPath,
		Lock:       runner.SharedLock(),
		// Matches this repository's Default Setup, which scans with remote *and local* sources.
		ThreatModel: "local",
	}
	if err := r.CreateDatabase(ctx, cfg.sourceRoot, dbDir, cfg.language); err != nil {
		return fmt.Errorf("selfcheck: creating a database: %w", err)
	}
	if err := r.AnalyzeDatabase(ctx, dbDir, cfg.suite, sarifPath); err != nil {
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
	return selfcheckVerdict(summary, cfg.maxFindings)
}

// selfcheckSummary is a pure aggregation of one SARIF document, kept separate from any I/O so
// it can be tested against a fixture instead of a real CodeQL run.
type selfcheckSummary struct {
	Rules      int
	Results    int
	BySeverity map[string]int
	// ByRule counts only rules that actually fired. A gate that says "21 findings" and not which
	// leaves the operator to re-run CodeQL by hand to learn what to fix.
	ByRule map[string]int
	// Locations holds where each rule fired, capped: a rule with 400 hits is a class of problem,
	// and printing all of them buries the other rules.
	Locations map[string][]string
}

// maxReportedLocations is how many places one rule names before the rest are left to CodeQL.
const maxReportedLocations = 5

// summarizeSARIF counts rules and findings by level, streaming rather than buffering: the same
// reasoning sarifin.Stream itself documents applies here even for one repository, since a
// broad security-and-quality suite can still produce a large document.
func summarizeSARIF(r io.Reader) (selfcheckSummary, error) {
	summary := selfcheckSummary{
		BySeverity: map[string]int{},
		ByRule:     map[string]int{},
		Locations:  map[string][]string{},
	}
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
			summary.ByRule[res.RuleID]++
			if res.FilePath != "" && len(summary.Locations[res.RuleID]) < maxReportedLocations {
				summary.Locations[res.RuleID] = append(summary.Locations[res.RuleID],
					fmt.Sprintf("%s:%d", res.FilePath, res.Region.StartLine))
			}
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
	if len(s.BySeverity) > 0 {
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

	ids := make([]string, 0, len(s.ByRule))
	for id := range s.ByRule {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(w, "  %s: %d\n", id, s.ByRule[id])
		for _, where := range s.Locations[id] {
			fmt.Fprintf(w, "      %s\n", where)
		}
		if extra := s.ByRule[id] - len(s.Locations[id]); extra > 0 && len(s.Locations[id]) > 0 {
			fmt.Fprintf(w, "      ... and %d more\n", extra)
		}
	}
}
