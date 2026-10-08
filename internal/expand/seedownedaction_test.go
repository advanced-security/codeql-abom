package expand

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// An owned repository used as an action by another is a dependency at the reference that uses it.
// Its node is the owned repository's own — internal, with no owner, its repo the full name — and
// queueing only external targets left `@v1` unresolved, so reach read the action at the owned
// repository's default branch while the submission closure, which needs the resolved commit, read
// nothing. A local `uses:` names the declaring repository's own commit and is still not queued.
func TestAnOwnedRepositoryUsedAsAnActionIsQueuedAtTheReferenceThatUsesIt(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("octo/app")
	action, err := h.store.UpsertRepository("octo/action", "")
	if err != nil {
		t.Fatal(err)
	}
	app, err := h.store.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(h.store, 0)
	workflow := "octo/app/.github/workflows/ci.yml"
	for _, n := range []store.Node{
		{Key: "octo/action", Kind: "composite_action", Repo: "octo/action", RepositoryID: action},
		{Key: "octo/app/.github/actions/local", Kind: "composite_action", Repo: "octo/app", RepositoryID: app},
		{Key: workflow, Kind: "workflow", RepositoryID: app},
		{Key: "octo/app:build:7", Kind: "step", RepositoryID: app, ContainerKey: workflow},
		{Key: "octo/app:build:9", Kind: "step", RepositoryID: app, ContainerKey: workflow},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []store.Edge{
		{Key: "octo/app:ci.yml:7:15", SrcKey: "octo/app:build:7", DstKey: "octo/action",
			Kind: store.EdgeKindStepUsesAction, RefRaw: "octo/action@v1", RefValue: "v1",
			FilePath: ".github/workflows/ci.yml", StartLine: 7, StartColumn: 15, RepositoryID: app},
		{Key: "octo/app:ci.yml:9:15", SrcKey: "octo/app:build:9", DstKey: "octo/app/.github/actions/local",
			Kind: store.EdgeKindStepUsesAction, RefRaw: "./.github/actions/local",
			FilePath: ".github/workflows/ci.yml", StartLine: 9, StartColumn: 15, RepositoryID: app},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}

	rows, err := h.store.DB().Query(`SELECT node_key, ref, owner, repo, COALESCE(subpath, ''), depth
		FROM abom_frontier ORDER BY node_key, ref`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var key, ref, owner, repo, subpath string
		var depth int
		if err := rows.Scan(&key, &ref, &owner, &repo, &subpath, &depth); err != nil {
			t.Fatal(err)
		}
		got = append(got, key+"@"+ref+" "+owner+"/"+repo+" path="+subpath+" depth="+string(rune('0'+depth)))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"octo/action@v1 octo/action path= depth=1"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("queued %q, want %q: the owned action at the reference that uses it, and not the local one",
			got, want)
	}
}
