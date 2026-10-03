package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// adder writes one node through w, failing the test at the first error.
func adder(t *testing.T, w *Writer) func(Node) {
	return func(n Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

// Indexes are built after loading, so the schema must be usable before they exist.
//
// The constraint is about the tables a crawl inserts millions of rows into: maintaining their
// indexes during the load costs far more than building them once at the end. It is not a ban on
// indexes as such, and reading it as one would leave a read-only server — which never calls
// CreateIndexes at all — running the Discovery page's provenance join as a full table scan.
func TestIndexesAreNotCreatedByMigrateOnTheTablesItLoadsInBulk(t *testing.T) {
	s := newTestStore(t)

	bulk := []string{"abom_edge", "abom_node", "abom_finding", "abom_finding_node"}
	for _, table := range bulk {
		var count int
		if err := s.DB().QueryRow(
			`SELECT COUNT(*) FROM sqlite_master
			 WHERE type='index' AND name LIKE 'idx_%' AND tbl_name = ?`, table).
			Scan(&count); err != nil {
			t.Fatalf("counting indexes on %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("Migrate indexed %s, which is loaded in bulk: %d indexes", table, count)
		}
	}

	var count int
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%'`).
		Scan(&count); err != nil {
		t.Fatalf("counting indexes: %v", err)
	}
	if count == 0 {
		t.Error("CreateIndexes created no indexes")
	}
}

func TestCreateIndexesIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("first CreateIndexes: %v", err)
	}
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("second CreateIndexes: %v", err)
	}
}

func seedGraph(t *testing.T, s *Store, w *Writer) int64 {
	t.Helper()
	repoID, err := s.UpsertRepository("octo-org/app", "https://github.com/octo-org/app")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	nodes := []Node{
		// Ingestion records the workflow holding each step, with the path findings are
		// reported against, so the fixture carries it too.
		{Key: "octo-org/app/.github/workflows/main.yml", Kind: "workflow",
			RepositoryID: repoID, Path: ".github/workflows/main.yml"},
		{Key: "step:octo-org/app:.github/workflows/main.yml:build:0", Kind: "step",
			RepositoryID: repoID, Path: ".github/workflows/main.yml",
			ContainerKey: "octo-org/app/.github/workflows/main.yml"},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout", IsExternal: true},
	}
	for _, n := range nodes {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key:        "octo-org/app:.github/workflows/main.yml:16:15",
		SrcKey:     nodes[1].Key,
		DstKey:     nodes[2].Key,
		Kind:       "step-uses-action",
		RefRaw:     "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
		RefValue:   "11bd71901bbe5b1630ceea73d27597364c9af683",
		PinComment: "v4.2.2",
		FilePath:   ".github/workflows/main.yml", StartLine: 16, StartColumn: 15,
		RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return repoID
}

func TestWriterInsertsNodesAndEdges(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))

	var nodes, edges int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node`).Scan(&nodes); err != nil {
		t.Fatalf("counting nodes: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edges); err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	if nodes != 3 || edges != 1 {
		t.Errorf("got %d nodes / %d edges, want 3 / 1", nodes, edges)
	}

	// Edges carry raw extraction only; any verdict about the reference lives in abom_finding.
	var refValue, pinComment string
	if err := s.DB().QueryRow(`SELECT ref_value, pin_comment FROM abom_edge`).
		Scan(&refValue, &pinComment); err != nil {
		t.Fatalf("reading edge: %v", err)
	}
	if refValue != "11bd71901bbe5b1630ceea73d27597364c9af683" || pinComment != "v4.2.2" {
		t.Errorf("refValue=%q pinComment=%q", refValue, pinComment)
	}
}

// Re-ingesting an unchanged repository must not duplicate rows, which is what allows the graph
// to accumulate across repeated scans.
func TestReingestIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	seedGraph(t, s, NewWriter(s, 0))

	var nodes, edges, repos int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node`).Scan(&nodes)
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edges)
	s.DB().QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&repos)

	if nodes != 3 || edges != 1 || repos != 1 {
		t.Errorf("after re-ingest got %d nodes / %d edges / %d repos, want 3 / 1 / 1",
			nodes, edges, repos)
	}
}

// A version bump changes the ref at the same source location; the edge must be updated in
// place rather than duplicated.
func TestEdgeUpsertUpdatesRefOnVersionBump(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	seedGraph(t, s, w)

	if err := w.AddNode(Node{Key: "actions/checkout", Kind: "action", IsExternal: true}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddEdge(Edge{
		Key:      "octo-org/app:.github/workflows/main.yml:16:15",
		SrcKey:   "step:octo-org/app:.github/workflows/main.yml:build:0",
		DstKey:   "actions/checkout",
		Kind:     "step-uses-action",
		RefRaw:   "actions/checkout@v5",
		RefValue: "v5",
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var edges int
	var refRaw, refValue string
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edges)
	if err := s.DB().QueryRow(`SELECT ref_raw, ref_value FROM abom_edge`).
		Scan(&refRaw, &refValue); err != nil {
		t.Fatalf("reading edge: %v", err)
	}
	if edges != 1 {
		t.Errorf("got %d edges, want 1", edges)
	}
	if refRaw != "actions/checkout@v5" || refValue != "v5" {
		t.Errorf("got refRaw=%q refValue=%q, want the bumped reference", refRaw, refValue)
	}
}

func TestEdgeWithUnknownNodeIsRejected(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)

	if err := w.AddNode(Node{Key: "a", Kind: "step"}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddEdge(Edge{Key: "e1", SrcKey: "a", DstKey: "missing", Kind: "x"}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err == nil {
		t.Fatal("expected an error for an edge referencing an unknown node")
	}
}

// Batching must not change the result, only when writes happen.
func TestSmallBatchSizeFlushesMidStream(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 2)

	for i, key := range []string{"n1", "n2", "n3", "n4", "n5"} {
		if err := w.AddNode(Node{Key: key, Kind: "step", StepIndex: i}); err != nil {
			t.Fatalf("AddNode %s: %v", key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var count int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node`).Scan(&count)
	if count != 5 {
		t.Errorf("got %d nodes, want 5", count)
	}
}

// ResetLocal must keep shared external nodes resolvable while dropping per-repo entries, so
// memory stays flat across an organization.
func TestResetLocalKeepsExternalNodesCached(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	seedGraph(t, s, w)

	w.ResetLocal()

	if _, ok := w.nodeIDs["actions/checkout"]; !ok {
		t.Error("external node was evicted from the cache")
	}
	if _, ok := w.nodeIDs["step:octo-org/app:.github/workflows/main.yml:build:0"]; ok {
		t.Error("repository-local node survived ResetLocal")
	}
}

func TestEmptyStringsStoredAsNull(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{Key: "n", Kind: "step"}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var ownerIsNull bool
	if err := s.DB().QueryRow(`SELECT owner IS NULL FROM abom_node WHERE node_key='n'`).
		Scan(&ownerIsNull); err != nil {
		t.Fatalf("reading node: %v", err)
	}
	if !ownerIsNull {
		t.Error("empty owner was stored as an empty string rather than NULL")
	}
}

func TestOptimizeSucceedsAfterIndexes(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	if err := s.Optimize(); err != nil {
		t.Fatalf("Optimize: %v", err)
	}
}

func TestUnitKeyIsStableAndDistinct(t *testing.T) {
	base := Unit{Repository: "o/r", DBCommit: "abc", QueryID: "actions/abom/edges",
		QueryVersion: "0.1.0", ExtractorVersion: "1"}

	first, second := base.Key(), base.Key()
	if first != second {
		t.Error("Key is not deterministic")
	}

	changed := base
	changed.ExtractorVersion = "2"
	if base.Key() == changed.Key() {
		t.Error("a new extractor version must produce a different key")
	}

	// Length prefixing keeps distinct field splits from colliding.
	a := Unit{Repository: "ab", QueryID: "c"}
	b := Unit{Repository: "a", QueryID: "bc"}
	if a.Key() == b.Key() {
		t.Error("field boundaries collided")
	}
}

func TestLedgerSkipsCompletedUnits(t *testing.T) {
	s := newTestStore(t)
	u := Unit{Repository: "o/r", DBCommit: "abc", QueryID: "q", ExtractorVersion: "1"}

	should, err := s.ShouldProcess(u)
	if err != nil {
		t.Fatalf("ShouldProcess: %v", err)
	}
	if !should {
		t.Fatal("an unseen unit should be processed")
	}

	if err := s.BeginUnit(u); err != nil {
		t.Fatalf("BeginUnit: %v", err)
	}
	if err := s.CompleteUnit(u, 42); err != nil {
		t.Fatalf("CompleteUnit: %v", err)
	}

	should, err = s.ShouldProcess(u)
	if err != nil {
		t.Fatalf("ShouldProcess: %v", err)
	}
	if should {
		t.Error("a completed unit must be skipped")
	}
}

func TestLedgerRetriesFailedUnits(t *testing.T) {
	s := newTestStore(t)
	u := Unit{Repository: "o/r", QueryID: "q"}

	if err := s.BeginUnit(u); err != nil {
		t.Fatalf("BeginUnit: %v", err)
	}
	if err := s.FailUnit(u, errors.New("boom")); err != nil {
		t.Fatalf("FailUnit: %v", err)
	}

	should, err := s.ShouldProcess(u)
	if err != nil {
		t.Fatalf("ShouldProcess: %v", err)
	}
	if !should {
		t.Error("a failed unit must be retried")
	}

	counts, err := s.LedgerCounts()
	if err != nil {
		t.Fatalf("LedgerCounts: %v", err)
	}
	if counts[StatusFailed] != 1 {
		t.Errorf("failed count = %d, want 1", counts[StatusFailed])
	}
}

// A commit change must invalidate prior work so version bumps are picked up.
func TestLedgerReprocessesOnNewCommit(t *testing.T) {
	s := newTestStore(t)
	u := Unit{Repository: "o/r", DBCommit: "commit1", QueryID: "q"}
	if err := s.BeginUnit(u); err != nil {
		t.Fatalf("BeginUnit: %v", err)
	}
	if err := s.CompleteUnit(u, 1); err != nil {
		t.Fatalf("CompleteUnit: %v", err)
	}

	next := u
	next.DBCommit = "commit2"
	should, err := s.ShouldProcess(next)
	if err != nil {
		t.Fatalf("ShouldProcess: %v", err)
	}
	if !should {
		t.Error("a new commit must be reprocessed")
	}
}

func TestBeginUnitClearsPreviousError(t *testing.T) {
	s := newTestStore(t)
	u := Unit{Repository: "o/r", QueryID: "q"}

	if err := s.BeginUnit(u); err != nil {
		t.Fatalf("BeginUnit: %v", err)
	}
	if err := s.FailUnit(u, errors.New("boom")); err != nil {
		t.Fatalf("FailUnit: %v", err)
	}
	if err := s.BeginUnit(u); err != nil {
		t.Fatalf("second BeginUnit: %v", err)
	}

	var errIsNull bool
	var status string
	if err := s.DB().QueryRow(
		`SELECT error IS NULL, status FROM abom_ingest_ledger WHERE unit_key = ?`, u.Key()).
		Scan(&errIsNull, &status); err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	if !errIsNull {
		t.Error("retry did not clear the previous error")
	}
	if status != StatusRunning {
		t.Errorf("status = %q, want %q", status, StatusRunning)
	}
}
