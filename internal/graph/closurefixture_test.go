package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// closureFixture separates "reachable from something declared" from "present in the graph".
//
//	octo-org/tool    ci.yml -> actions/checkout
//	octo-org/app     ci.yml -> octo-org/shared
//	octo-org/shared  ci.yml -> far/away
//	stranger/repo    ci.yml -> stranger/only-they-use-this
//	stranger/isolated                              (names nothing, named by nothing)
//	octo-org/tool    ci.yml =needs=> ordered/after (ordering, never a dependency)
func closureFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repos := map[string]int64{}
	for _, name := range []string{
		"octo-org/app", "octo-org/tool", "octo-org/shared",
		"stranger/repo", "stranger/isolated",
	} {
		id, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", name, err)
		}
		repos[name] = id
	}

	w := store.NewWriter(s, 0)
	add := func(n store.Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	for name, id := range repos {
		add(store.Node{Key: name + "/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: id})
	}
	for _, target := range []struct{ key, owner, repo string }{
		{"actions/checkout", "actions", "checkout"},
		{"octo-org/shared", "octo-org", "shared"},
		{"far/away", "far", "away"},
		{"stranger/only-they-use-this", "stranger", "only-they-use-this"},
		{"ordered/after", "ordered", "after"},
	} {
		add(store.Node{
			Key: target.key, Kind: "action", Owner: target.owner, Repo: target.repo,
			IsExternal: true,
		})
	}

	edges := []struct{ key, src, dst, kind, from string }{
		{"e1", "octo-org/tool/.github/workflows/ci.yml", "actions/checkout",
			"step-uses-action", "octo-org/tool"},
		{"e2", "octo-org/app/.github/workflows/ci.yml", "octo-org/shared",
			"step-uses-action", "octo-org/app"},
		{"e3", "octo-org/shared/.github/workflows/ci.yml", "far/away",
			"step-uses-action", "octo-org/shared"},
		{"e4", "stranger/repo/.github/workflows/ci.yml", "stranger/only-they-use-this",
			"step-uses-action", "stranger/repo"},
		{"e5", "octo-org/tool/.github/workflows/ci.yml", "ordered/after",
			"job-needs-job", "octo-org/tool"},
	}
	for _, e := range edges {
		if err := w.AddEdge(store.Edge{
			Key: e.key, SrcKey: e.src, DstKey: e.dst, Kind: e.kind,
			RepositoryID: repos[e.from], FilePath: ".github/workflows/ci.yml", StartLine: 1,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}
