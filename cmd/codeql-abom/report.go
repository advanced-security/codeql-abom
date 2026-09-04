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

	items, err := s.RemediationPriority(*top)
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

func writeRemediation(w io.Writer, items []store.RemediationItem) {
	fmt.Fprintln(w)
	if len(items) == 0 {
		fmt.Fprintln(w, "Remediation priorities: none")
		return
	}
	fmt.Fprintln(w, "Remediation priorities (severity x reach):")
	for _, it := range items {
		id := it.GHSAID
		if it.CVEID != "" {
			id = fmt.Sprintf("%s (%s)", id, it.CVEID)
		}
		fmt.Fprintf(w, "  [%d] %s %s - %d direct, %d transitive repositories\n",
			it.Priority, id, it.Severity, it.DirectRepos, it.TransitiveRepos)
		if it.Summary != "" {
			fmt.Fprintf(w, "        %s\n", it.Summary)
		}
		if len(it.Actions) > 0 {
			fmt.Fprintf(w, "        actions: %v\n", it.Actions)
		}
	}
}
