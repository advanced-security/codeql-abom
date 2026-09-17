package store

import (
	"path/filepath"
	"testing"
)

// pruneFixture separates what an owned repository reaches from what nobody owned does.
//
//	octo-org/app    ci.yml -> actions/checkout      (inside)
//	stranger/repo   ci.yml -> stranger/thing        (outside)
func pruneFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	stranger, err := s.UpsertRepository("stranger/repo", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := NewWriter(s, 0)
	add := func(n Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	add(Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app})
	add(Node{Key: "stranger/repo/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: stranger})
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})
	add(Node{Key: "stranger/thing", Kind: "action", Owner: "stranger", Repo: "thing",
		IsExternal: true})

	for _, e := range []Edge{
		{Key: "in", SrcKey: "octo-org/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
			Kind: "step-uses-action", RepositoryID: app,
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
		{Key: "out", SrcKey: "stranger/repo/.github/workflows/ci.yml", DstKey: "stranger/thing",
			Kind: "step-uses-action", RepositoryID: stranger,
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// A generation, so a test has a real cursor to poll from rather than 0, which is no cursor
	// at all and correctly answers Reset.
	if _, err := s.RecordGraphGeneration(GraphRebuild{}); err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	// A run and a finding, so the ledger's survival is observable.
	run, err := s.RecordQueryRun(QueryRun{QueryID: "abom/edges", Repository: "stranger/repo"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		QueryRunID: run, RuleID: "actions/unpinned-tag", RepositoryID: stranger,
		FilePath: ".github/workflows/ci.yml", StartLine: 1,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	return s
}
