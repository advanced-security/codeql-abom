package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A generation cursor is a row id watermark, which only means anything while a client is told
// when rows go away. An unannounced DELETE would silently hand every client a delta that skips
// or repeats work, so the absence is pinned here rather than left to review.
//
// Narrowed on 2026-09-07 from "nothing may delete" to "only the compaction may". The stated
// reason was always the cursor, not deletion as such: `store/prune.go` bumps an epoch that makes
// every client reload, which is precisely what the cursor needed and what nothing else does.
// Deleting anywhere else is still the defect this was written for.
func TestGraphTablesAreDeletedOnlyByTheCompaction(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}
	appendOnly := []string{"abom_node", "abom_edge", "abom_finding"}
	// The one file allowed to delete, because it is the one that ends the cursor's meaning.
	const compaction = "internal/store/prune.go"

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if filepath.ToSlash(rel) == compaction {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, table := range appendOnly {
			// A word boundary, or `abom_finding` matches `abom_finding_node`, which is a link
			// table and not one of these.
			deletes := regexp.MustCompile(`DELETE FROM ` + table + `\b`)
			if deletes.MatchString(string(body)) {
				t.Errorf("%s deletes from %s without bumping the epoch, "+
					"which invalidates every delta cursor in silence", rel, table)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// Reach is recomputed wholesale, so a node ingested since the last rebuild has no row to read.
// Reporting that absence as zero would claim nothing depends on it, which is a measurement
// nobody made and, in a view that sizes nodes by reach, one a reader will believe.
func TestUnmeasuredReachIsNeverReportedAsZero(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("no nodes to check")
	}
	for _, n := range g.Nodes {
		if n.Kind == OrgNodeKind {
			continue
		}
		if n.ReachKnown {
			t.Errorf("%s reports measured reach before any rebuild ran", n.Key)
		}
	}
}

// Only nodes something points at get a blast radius row, so a missing row is usually a
// measurement of zero rather than an absence of one. Treating every one of them as unknown
// would say nothing is known about most of the corpus.
func TestAMeasuredNodeNothingReferencesReportsKnownReach(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if !n.ReachKnown {
			t.Errorf("%s reports unmeasured reach after a rebuild that covered it", n.Key)
		}
	}
}

// A node that arrived after the rebuild is the one case where the absence really is an absence.
func TestANodeAddedAfterTheRebuildReportsUnmeasuredReach(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	w := NewWriter(s, 0)
	if err := w.AddNode(Node{
		Key: "octo-org/app/.github/workflows/arrived.yml", Kind: "workflow",
		RepositoryID: repoID, Path: ".github/workflows/arrived.yml",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Key == "octo-org/app/.github/workflows/arrived.yml" && n.ReachKnown {
			t.Error("a node added after the rebuild claims its reach was measured")
		}
	}
}

func TestGraphDeltaReturnsOnlyWhatArrivedAfterTheGeneration(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/later.yml", Kind: "workflow",
			RepositoryID: repoID, Path: ".github/workflows/later.yml"},
		{Key: "step:octo-org/app:.github/workflows/later.yml:build:0", Kind: "step",
			RepositoryID: repoID, Path: ".github/workflows/later.yml",
			ContainerKey: "octo-org/app/.github/workflows/later.yml"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{
		Key:    "octo-org/app:.github/workflows/later.yml:3:9",
		SrcKey: "step:octo-org/app:.github/workflows/later.yml:build:0",
		DstKey: "actions/cache", Kind: "step-uses-action",
		FilePath: ".github/workflows/later.yml", StartLine: 3, RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	delta, err := s.GraphDelta(generation, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Fatal("asked for a reload for a generation it recorded itself")
	}

	keys := map[string]bool{}
	for _, n := range delta.Nodes {
		keys[n.Key] = true
	}
	if !keys["octo-org/app/.github/workflows/later.yml"] || !keys["actions/cache"] {
		t.Errorf("delta nodes = %v, want the workflow and action added after the generation", keys)
	}
	if keys["octo-org/app/.github/workflows/main.yml"] {
		t.Error("delta carried a node that existed before the generation")
	}
	// Metrics cover nodes the client already holds, because reach moves without the node
	// itself changing.
	measured := map[string]bool{}
	for _, m := range delta.Metrics {
		measured[m.Key] = true
	}
	if !measured["octo-org/app/.github/workflows/main.yml"] {
		t.Errorf("metrics = %v, want them to cover a node the client already holds", measured)
	}
}

func TestGraphDeltaAsksForAReloadWhenTheCursorIsUnknown(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))

	delta, err := s.GraphDelta(999, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if !delta.Reset {
		t.Error("accepted a generation it never issued instead of asking for a reload")
	}
}
