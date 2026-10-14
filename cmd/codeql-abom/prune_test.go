package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Explaining by default is the safety property. A destructive operation whose scope is visible
// only afterwards is one nobody will run twice.
func TestPruneExplainsWithoutChangingAnything(t *testing.T) {
	db := prunableDB(t)
	before := liveNodeCount(t, db)

	out := runPruneOK(t, "--db", db)

	if !strings.Contains(out, "would remove") {
		t.Errorf("output = %q, want it to say what would go", out)
	}
	if !strings.Contains(out, "--apply") {
		t.Error("a dry run that does not say how to act on it is a dead end")
	}
	if after := liveNodeCount(t, db); after != before {
		t.Errorf("an explanation removed rows: %d became %d", before, after)
	}
}

func TestPruneMarksOnlyWhenAsked(t *testing.T) {
	db := prunableDB(t)
	before := liveNodeCount(t, db)

	runPruneOK(t, "--db", db, "--apply")

	if after := liveNodeCount(t, db); after >= before {
		t.Errorf("live nodes %d -> %d; --apply should have marked what is outside", before, after)
	}
}

// Pruning against an undeclared scope would remove the entire corpus on the strength of a
// configuration nobody has written yet.
func TestPruneRefusesWhenNothingIsDeclared(t *testing.T) {
	db := prunableDB(t)
	clearScope(t, db)
	before := liveNodeCount(t, db)

	var out bytes.Buffer
	err := runPrune(context.Background(), []string{"--db", db, "--apply"}, &out, &out)

	if err == nil {
		t.Fatal("pruning with nothing declared must be refused")
	}
	if after := liveNodeCount(t, db); after != before {
		t.Errorf("the refused prune removed rows anyway: %d became %d", before, after)
	}
}

// A removal of most of what is in view is what a scope that could not be confirmed produces — a
// listing that under-reported, an organization a `gh` check could not see — as readily as one that
// owns little. It is applied only when that is said outright.
func TestPruneRefusesToRemoveMostOfTheGraphUnlessToldTo(t *testing.T) {
	db := prunableDB(t)
	addOutside(t, db, 4)
	before := liveNodeCount(t, db)

	var out bytes.Buffer
	err := runPrune(context.Background(), []string{"--db", db, "--apply"}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "--most") {
		t.Fatalf("a removal of most of the graph was not refused with the way to say it: %v\n%s", err, out.String())
	}
	if after := liveNodeCount(t, db); after != before {
		t.Fatalf("the refused removal removed rows anyway: %d became %d", before, after)
	}

	runPruneOK(t, "--db", db, "--apply", "--most")
	if after := liveNodeCount(t, db); after >= before {
		t.Errorf("live nodes %d -> %d; --apply --most should have marked what is outside", before, after)
	}
}

// Compaction is the irreversible half, so it is its own flag rather than something --apply
// escalates into.
func TestCompactingIsSeparateFromMarking(t *testing.T) {
	db := prunableDB(t)
	runPruneOK(t, "--db", db, "--apply")
	rows := allNodeCount(t, db)

	out := runPruneOK(t, "--db", db, "--compact")

	if !strings.Contains(out, "reload") {
		t.Errorf("output = %q, want it to warn that clients reload", out)
	}
	if after := allNodeCount(t, db); after >= rows {
		t.Errorf("physical rows %d -> %d; compaction should have deleted the tombstoned ones",
			rows, after)
	}
}

func prunableDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seedPrunable(t, s)
	s.Close()
	return path
}

// seedPrunable gives the corpus one declared repository and one nothing owned reaches.
func seedPrunable(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo-org", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	mine, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	theirs, err := s.UpsertRepository("stranger/repo", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: mine},
		{Key: "octo-org/app/.github/workflows/release.yml", Kind: "workflow", RepositoryID: mine},
		{Key: "stranger/repo/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: theirs},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout", IsExternal: true},
		{Key: "stranger/thing", Kind: "action", Owner: "stranger", Repo: "thing", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	for _, e := range []store.Edge{
		{Key: "in", SrcKey: "octo-org/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
			Kind: "step-uses-action", RepositoryID: mine,
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
		{Key: "in-release", SrcKey: "octo-org/app/.github/workflows/release.yml", DstKey: "actions/checkout",
			Kind: "step-uses-action", RepositoryID: mine,
			FilePath: ".github/workflows/release.yml", StartLine: 1},
		{Key: "out", SrcKey: "stranger/repo/.github/workflows/ci.yml", DstKey: "stranger/thing",
			Kind: "step-uses-action", RepositoryID: theirs,
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// addOutside puts n more workflows in the repository nothing owned reaches.
func addOutside(t *testing.T, db string, n int) {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	theirs, err := s.UpsertRepository("stranger/repo", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	for i := 0; i < n; i++ {
		if err := w.AddNode(store.Node{
			Key:  fmt.Sprintf("stranger/repo/.github/workflows/extra-%d.yml", i),
			Kind: "workflow", RepositoryID: theirs,
		}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func clearScope(t *testing.T, db string) {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	if err := s.SetOwnedScope(nil, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func runPruneOK(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPrune(context.Background(), args, &out, &out); err != nil {
		t.Fatalf("prune %v: %v (%s)", args, err, out.String())
	}
	return out.String()
}

func liveNodeCount(t *testing.T, db string) int { return nodeCount(t, db, "abom_node_live") }
func allNodeCount(t *testing.T, db string) int  { return nodeCount(t, db, "abom_node") }

func nodeCount(t *testing.T, db, table string) int {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}
