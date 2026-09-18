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

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The account the fixture repositories belong to. Declared rather than inferred, because nothing
// in ABOM happens until an operator claims ownership and a corpus that skipped that step would
// exercise a path no install takes.
const fixtureOwner = "octo-org"

func main() {
	dbPath := flag.String("db", "", "path to write (required)")
	fixtures := flag.String("fixtures", "testdata/corpus/fixtures", "recorded CodeQL output")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "abom-corpus: --db is required")
		os.Exit(2)
	}
	if err := build(*dbPath, *fixtures); err != nil {
		fmt.Fprintf(os.Stderr, "abom-corpus: %v\n", err)
		os.Exit(1)
	}
}

func build(dbPath, fixtures string) error {
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

	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: fixtureOwner, Kind: "organization", Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		return fmt.Errorf("declaring %s: %w", fixtureOwner, err)
	}

	for _, r := range repos {
		if err := ingestRepository(s, fixtures, r); err != nil {
			return fmt.Errorf("%q: %w", r, err)
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

func ingestRepository(s *store.Store, fixtures, full string) error {
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

	sarif, err := os.Open(filepath.Join(dir, "findings.sarif"))
	if err != nil {
		return err
	}
	defer sarif.Close()
	if _, err := ing.FindingsFromSARIF(sarif, ingest.Options{Repository: full}); err != nil {
		return fmt.Errorf("findings: %w", err)
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
