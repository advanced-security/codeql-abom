package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// runReport prints a human-readable summary of what has been collected so far.
func runReport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	top := fs.Int("top", 10, "number of remediation priorities to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("report: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	summary, err := s.Summary()
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	writeSummary(stdout, summary)

	totals, err := s.PinningTotals(store.PinningFilter{})
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	verified, err := s.Verified()
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	writePinning(stdout, totals, verified)

	items, err := s.RemediationPriority(*top, store.RepositoryScope{})
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	writeRemediation(stdout, items)
	return nil
}

func writeSummary(w io.Writer, s store.Summary) {
	fmt.Fprintf(w, "Repositories:     %d under review, %d upstream (%d analyzed)\n",
		s.SeedRepos, s.UpstreamRepos, s.AnalyzedUpstreams)
	fmt.Fprintf(w, "Graph:            %d nodes, %d edges\n", s.Nodes, s.Edges)
	fmt.Fprintf(w, "Findings:         %d across %d rules (%d linked to the graph)\n",
		s.Findings, s.Rules, s.LinkedFindings)
	fmt.Fprintf(w, "Advisories:       %d, covering %d actions\n", s.Advisories, s.AffectedActions)
	fmt.Fprintf(w, "Exposure:         %d repositories (%d direct)\n", s.Exposures, s.DirectExposures)

	if len(s.Frontier) == 0 {
		fmt.Fprintln(w, "Frontier:         empty")
		return
	}
	statuses := make([]string, 0, len(s.Frontier))
	for status := range s.Frontier {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	fmt.Fprint(w, "Frontier:        ")
	for _, status := range statuses {
		fmt.Fprintf(w, " %s=%d", status, s.Frontier[status])
	}
	fmt.Fprintln(w)
}

// writePinning reports how references are written and, separately, what the rule objected to.
//
// The two are never combined into one figure. How a reference is written is an observation;
// whether it is safely pinned is `actions/unpinned-tag`'s call, and the rule exempts
// GitHub-owned actions, so the two disagree by thousands of references on a real corpus.
func writePinning(w io.Writer, total store.PinningRow, verified bool) {
	fmt.Fprintln(w)
	if total.Pinnable == 0 {
		fmt.Fprintln(w, "Reference pinning: no pinnable references in scope.")
		fmt.Fprintln(w, "  That is not the same as everything being pinned.")
		return
	}

	fmt.Fprintln(w, "Reference pinning:")
	fmt.Fprintf(w, "  %d of %d pinnable references name a commit (%.1f%%)\n",
		total.Commit, total.Pinnable, 100*float64(total.Commit)/float64(total.Pinnable))
	fmt.Fprintf(w, "  actions/unpinned-tag flagged %d; %d more name a tag but sit outside "+
		"its scope\n", total.Flagged, total.FloatingUnflagged)

	if !verified {
		fmt.Fprintln(w, "  which of those are yours is unknown: no accounts verified")
		fmt.Fprintln(w, "    run: codeql-abom accounts --db <path> --refresh")
		return
	}
	fmt.Fprintf(w, "  of those flagged, %d are in accounts you belong to and %d are elsewhere\n",
		total.FlaggedSplit.Yours, total.FlaggedSplit.Elsewhere)
}

func writeRemediation(w io.Writer, items []store.RemediationItem) {
	fmt.Fprintln(w)
	if len(items) == 0 {
		fmt.Fprintln(w, "Remediation priorities: none")
		return
	}
	fmt.Fprintln(w, "Remediation priorities (severity, then reach):")
	for _, it := range items {
		id := it.GHSAID
		if it.CVEID != "" {
			id = fmt.Sprintf("%s (%s)", id, it.CVEID)
		}
		fmt.Fprintf(w, "  [%d] %s %s - %d direct, %d transitive repositories\n",
			it.Rank, id, it.Severity, it.DirectRepos, it.TransitiveRepos)
		if it.Summary != "" {
			fmt.Fprintf(w, "        %s\n", it.Summary)
		}
		if len(it.Actions) > 0 {
			fmt.Fprintf(w, "        actions: %v\n", it.Actions)
		}
	}
}
