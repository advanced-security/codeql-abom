// Command abom-corpus builds a populated ABOM database from the fixtures under testdata/corpus.
//
// Separate from the shipped binary because it is a development tool, and offline by construction:
// the expensive, non-deterministic half of a corpus is CodeQL, and its output is recorded beside
// the source trees by scripts/regenerate-corpus-fixtures.sh. Rebuilding is then pure Go, so CI can
// hold a populated database rather than gating only the empty half.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func main() {
	dbPath := flag.String("db", "", "path to write (required)")
	fixtures := flag.String("fixtures", "testdata/corpus/fixtures", "recorded CodeQL output")
	// The states a component catalog would otherwise exist to draw. A database nobody can build is
	// the only reason to keep a second rendering of every component alive.
	without := flag.String("without", "",
		"comma-separated facts to leave out: advisories, findings")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "abom-corpus: --db is required")
		os.Exit(2)
	}
	if err := build(*dbPath, *fixtures, omitted(*without)); err != nil {
		fmt.Fprintf(os.Stderr, "abom-corpus: %v\n", err)
		os.Exit(1)
	}
}

// omitted reads the --without list into a set.
func omitted(list string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	return out
}

func build(dbPath, fixtures string, without map[string]bool) error {
	// A rebuild replaces rather than merges: a corpus that accumulated across runs would differ
	// between a fresh checkout and a second invocation, which is the property this exists to have.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	s, err := store.Open(dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %q: %w", dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating: %w", err)
	}

	repos, err := recorded(fixtures)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("no fixtures under %q; run scripts/regenerate-corpus-fixtures.sh", fixtures)
	}

	if err := s.SetOwnedScope(declaredAccounts(repos), store.DeclaredByCLI); err != nil {
		return fmt.Errorf("declaring the fixture accounts: %w", err)
	}

	for _, r := range repos {
		if err := ingestRepository(s, fixtures, r, without); err != nil {
			return fmt.Errorf("%q: %w", r, err)
		}
	}

	// Beside the recorded queries rather than among them: an advisory is not CodeQL output, so no
	// number of repositories produces one. Without it the advisories unit is empty, its columns
	// are all zero, and four browser assertions can never go green.
	if !without["advisories"] {
		if err := recordAdvisories(s, filepath.Join(filepath.Dir(fixtures), "advisories.json")); err != nil {
			return err
		}
	}

	// Order matters: boundaries need the edges, findings need the nodes, and the aggregates need
	// both. This is the same sequence a crawl ends with.
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		return fmt.Errorf("classifying boundaries: %w", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		return fmt.Errorf("linking findings: %w", err)
	}
	if err := (&graph.Builder{Store: s}).RebuildAggregates(); err != nil {
		return fmt.Errorf("rebuilding aggregates: %w", err)
	}

	return report(os.Stdout, s, dbPath)
}

// declaredAccounts names every account the fixtures describe.
//
// Read from the trees rather than fixed in code, so adding an account is adding a directory.
// Declared rather than inferred, because nothing in ABOM happens until an operator claims
// ownership, and a data set that skipped that step would exercise a path no install takes.
// More than one, because an estate of a single account cannot exercise anything that compares
// two — and the interface groups by account throughout.
func declaredAccounts(repos []string) []store.DeclaredAccount {
	seen := map[string]bool{}
	owners := []string{}
	for _, r := range repos {
		owner, _, ok := strings.Cut(r, "/")
		if !ok || seen[owner] {
			continue
		}
		seen[owner] = true
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	out := make([]store.DeclaredAccount, 0, len(owners))
	for _, owner := range owners {
		out = append(out, store.DeclaredAccount{
			Owner: owner, Kind: "organization", Enabled: true,
		})
	}
	return out
}

func ingestRepository(s *store.Store, fixtures, full string, without map[string]bool) error {
	if _, err := s.InsertOwnedRepository(full); err != nil {
		return fmt.Errorf("recording ownership: %w", err)
	}

	dir := filepath.Join(fixtures, strings.ReplaceAll(full, "/", "__"))
	w := store.NewWriter(s, 0)
	ing := ingest.New(s, w)

	edges, err := os.Open(filepath.Join(dir, "edges.json"))
	if err != nil {
		return err
	}
	defer edges.Close()
	if _, err := ing.EdgesFromBQRS(edges, ingest.Options{
		Repository:    full,
		RepositoryURL: "https://github.com/" + full,
		Provenance:    "corpus-fixture",
	}); err != nil {
		return fmt.Errorf("edges: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing edges: %w", err)
	}

	// Skipped rather than ingested and hidden: --without=findings exists to render the state of
	// a corpus that has none, and a database that held them anyway would render the other one.
	if !without["findings"] {
		sarif, err := os.Open(filepath.Join(dir, "findings.sarif"))
		if err != nil {
			return err
		}
		defer sarif.Close()
		if _, err := ing.FindingsFromSARIF(sarif, ingest.Options{Repository: full}); err != nil {
			return fmt.Errorf("findings: %w", err)
		}
	}

	// Recorded later than the rest, so a fixture written before it stays loadable rather than
	// failing the whole build on a file nobody regenerated yet.
	surface, err := os.Open(filepath.Join(dir, "surface.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer surface.Close()

	// Every observation names the run that produced it. This one replays a recording, so it says
	// so and leaves the CLI version empty: nothing here witnessed which CodeQL wrote the file,
	// and naming one would attest to something nobody checked.
	runID, err := s.RecordQueryRun(store.QueryRun{
		QueryID:      "abom/surface",
		QueryVersion: "corpus-fixture",
		Repository:   full,
	})
	if err != nil {
		return fmt.Errorf("recording the fixture run: %w", err)
	}
	if _, err := ing.ObservationsFromBQRS(surface, ingest.Options{
		Repository: full, QueryRunID: runID,
	}); err != nil {
		return fmt.Errorf("surface: %w", err)
	}
	return nil
}

// recordAdvisories stores a recorded GHSA response, which is the one fact the recorded CodeQL
// output cannot carry.
//
// Absent rather than empty is a legitimate corpus: an operator who has never refreshed advisories
// has none, and that has to stay buildable. A malformed one is not, so it is reported.
func recordAdvisories(s *store.Store, path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading advisories: %w", err)
	}
	defer f.Close()

	advisories, err := advisory.Parse(f)
	if err != nil {
		return fmt.Errorf("parsing %q: %w", path, err)
	}
	if _, err := s.AddAdvisories(ingest.Advisories(advisories)); err != nil {
		return fmt.Errorf("recording advisories: %w", err)
	}
	return nil
}

// recorded lists the repositories the fixture directory holds, as `owner/name`.
func recorded(fixtures string) ([]string, error) {
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", fixtures, err)
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, strings.Replace(e.Name(), "__", "/", 1))
	}
	sort.Strings(out)
	return out, nil
}

// report prints what was built, because a builder that says only "done" is one nobody notices has
// started producing an empty database.
//
// The path is quoted rather than interpolated: it arrives from a flag, and a newline in it would
// otherwise forge a line that reads like the tool's own.
func report(w io.Writer, s *store.Store, dbPath string) error {
	for _, c := range []struct{ label, query string }{
		{"repositories", `SELECT COUNT(*) FROM repository`},
		{"owned", `SELECT COUNT(*) FROM repository WHERE origin = 'seed'`},
		{"nodes", `SELECT COUNT(*) FROM abom_node_live`},
		{"edges", `SELECT COUNT(*) FROM abom_edge_live`},
		{"findings", `SELECT COUNT(*) FROM abom_finding`},
		{"findings on a node", `SELECT COUNT(DISTINCT finding_row_id) FROM abom_finding_node`},
		{"advisories", `SELECT COUNT(*) FROM abom_advisory`},
		// Reaching is the number that decides whether the unit draws anything, and it is the one
		// that was silently zero while the data set grew.
		{"advisories reaching", `SELECT COUNT(DISTINCT a.ghsa_id) FROM abom_advisory a
			JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
			JOIN abom_node_live n ON n.owner || '/' || n.repo = p.package_name`},
		{"job facts", `SELECT COUNT(*) FROM abom_observation`},
		{"privileged jobs",
			`SELECT COUNT(*) FROM abom_observation WHERE name = 'privileged' AND value = 'true'`},
	} {
		var n int
		if err := s.DB().QueryRow(c.query).Scan(&n); err != nil {
			return fmt.Errorf("counting %s: %w", c.label, err)
		}
		fmt.Fprintf(w, "%-20s %5d\n", c.label, n)
	}
	fmt.Fprintf(w, "corpus: %q\n", dbPath)
	return nil
}
