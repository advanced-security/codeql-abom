package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The corpus exists to make before/after claims about real rows possible at all, so what it must
// contain is not "some data" but the specific shapes the ranked work needs. Each assertion below
// names one of them.
func builtCorpus(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	if err := build(path, filepath.Join("..", "..", "testdata", "corpus", "fixtures")); err != nil {
		t.Fatalf("build: %v", err)
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("opening the corpus: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func count(t *testing.T, s *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil && err != sql.ErrNoRows {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The shape the crawled organization never had: every candidate there turned out to be a
// repository using its own composite action, so the rule about excluding a repository grouping
// had no data behind it.
func TestTheCorpusHoldsADependencyBetweenTwoDistinctOwnedRepositories(t *testing.T) {
	s := builtCorpus(t)

	n := count(t, s, `
		SELECT COUNT(*)
		FROM abom_edge e
		JOIN repository src ON src.row_id = e.repository_row_id
		JOIN abom_node dst  ON dst.row_id = e.dst_node_row_id
		JOIN repository dr  ON dr.repository_full_name = dst.owner || '/' || dst.repo
		WHERE src.origin = 'seed' AND dr.origin = 'seed'
		  AND LOWER(src.repository_full_name) <> LOWER(dr.repository_full_name)`)
	if n == 0 {
		t.Error("no edge runs between two distinct owned repositories")
	}
}

// All four, because `DependencyEdgeFilter` excludes one of them from every traversal and nothing
// can exercise that rule against a corpus holding only the other three.
func TestTheCorpusHoldsEveryEdgeKind(t *testing.T) {
	s := builtCorpus(t)

	for _, kind := range []string{
		"step-uses-action", "job-needs-job",
		"job-uses-reusable-workflow", "composite-step-uses-action",
	} {
		if count(t, s, `SELECT COUNT(*) FROM abom_edge WHERE edge_kind = ?`, kind) == 0 {
			t.Errorf("no %s edge", kind)
		}
	}
}

// Both readings, so a filter on how something is referenced has something to separate.
func TestTheCorpusHoldsBothReferenceStyles(t *testing.T) {
	s := builtCorpus(t)

	pinned := count(t, s,
		`SELECT COUNT(*) FROM abom_edge WHERE ref_value GLOB '[0-9a-f]*' AND LENGTH(ref_value) = 40`)
	floating := count(t, s,
		`SELECT COUNT(*) FROM abom_edge WHERE ref_value LIKE 'v%'`)
	if pinned == 0 || floating == 0 {
		t.Errorf("%d commit-pinned and %d floating references, want both", pinned, floating)
	}
}

// The experimental suite is what makes a clean repository distinguishable from an unanalyzed one,
// so a corpus seeded without it cannot exercise any finding path.
func TestEveryCorpusFindingIsAbomsOwnAndReachesANode(t *testing.T) {
	s := builtCorpus(t)

	total := count(t, s, `SELECT COUNT(*) FROM abom_finding`)
	if total == 0 {
		t.Fatal("no findings, so the experimental suite did not run")
	}
	if foreign := count(t, s,
		`SELECT COUNT(*) FROM abom_finding WHERE rule_id NOT LIKE 'abom/%'`); foreign != 0 {
		t.Errorf("%d findings are not ABOM's own", foreign)
	}
	// Every one, here: each fixture repository holds a `uses:` line, so every job and composite
	// action is a node the marker can attach to. The case that cannot attach — a composite action
	// referencing nothing — is held separately in internal/ingest/marker_test.go.
	orphaned := count(t, s, `
		SELECT COUNT(*) FROM abom_finding f
		WHERE NOT EXISTS (SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)`)
	if orphaned != 0 {
		t.Errorf("%d of %d findings reach no node", orphaned, total)
	}
}

// A rebuild replaces rather than accumulates, or a second run and a fresh checkout disagree.
func TestBuildingTwiceProducesTheSameCorpus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	fixtures := filepath.Join("..", "..", "testdata", "corpus", "fixtures")

	read := func() [2]int {
		if err := build(path, fixtures); err != nil {
			t.Fatalf("build: %v", err)
		}
		s, err := store.Open(path, store.Options{})
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		defer s.Close()
		return [2]int{
			count(t, s, `SELECT COUNT(*) FROM abom_node_live`),
			count(t, s, `SELECT COUNT(*) FROM abom_edge_live`),
		}
	}
	if first, second := read(), read(); first != second {
		t.Errorf("rebuild changed the corpus: %v then %v", first, second)
	}
}
