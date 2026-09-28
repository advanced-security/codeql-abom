package store

import (
	"strings"
	"testing"
)

// Who runs a version lives on the edges, not on the frontier's single first-discoverer.
func TestARowNamesEveryRepositoryUsingThatVersion(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{
		Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true,
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	for i, name := range []string{"octo/app", "octo/tools"} {
		repoID, err := s.UpsertRepository(name, OriginSeed)
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", name, err)
		}
		workflow := name + "/.github/workflows/ci.yml"
		if err := w.AddNode(Node{
			Key: workflow, Kind: "workflow", RepositoryID: repoID,
		}); err != nil {
			t.Fatalf("AddNode(%s): %v", workflow, err)
		}
		if err := w.AddEdge(Edge{
			Key: "e" + name, SrcKey: workflow,
			DstKey: "actions/checkout", Kind: "step-uses-action", RefValue: "v4",
			FilePath: "ci.yml", StartLine: i + 1, RepositoryID: repoID,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", name, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if item.UsedByCount != 2 {
		t.Errorf("UsedByCount = %d, want both repositories", item.UsedByCount)
	}
	joined := strings.Join(item.UsedBy, " ")
	for _, want := range []string{"octo/app", "octo/tools"} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %s named, got %v", want, item.UsedBy)
		}
	}
}

// Total users and operator-owned users are different decisions. A declared account's repository is
// one the operator can change; the rest are context.
func TestARowSeparatesTheRepositoriesTheOperatorOwns(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	seedUsers(t, s, "v4", "octo/app", "stranger/app")

	item := onlyFrontierItem(t, s)
	if item.UsedByCount != 2 {
		t.Errorf("UsedByCount = %d, want both", item.UsedByCount)
	}
	if item.UsedByOwned != 1 {
		t.Errorf("UsedByOwned = %d, want only the declared account's repository", item.UsedByOwned)
	}
}

// seedUsers gives actions/checkout one queue row at ref, named by each repository.
func seedUsers(t *testing.T, s *Store, ref string, repos ...string) {
	t.Helper()
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{
		Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true,
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	for i, name := range repos {
		repoID, err := s.UpsertRepository(name, OriginSeed)
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", name, err)
		}
		workflow := name + "/.github/workflows/ci.yml"
		if err := w.AddNode(Node{Key: workflow, Kind: "workflow", RepositoryID: repoID}); err != nil {
			t.Fatalf("AddNode(%s): %v", workflow, err)
		}
		if err := w.AddEdge(Edge{
			Key: "e" + name, SrcKey: workflow, DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: ref, FilePath: "ci.yml", StartLine: i + 1,
			RepositoryID: repoID,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", name, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', ?, 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
		ref); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
}

// A version nobody names is a real state — an upstream project's own CI reaches it — and must
// not borrow the users of a different version of the same action.
func TestUsersDoNotLeakBetweenVersionsOfOneAction(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{
		Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true,
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	repoID, err := s.UpsertRepository("octo/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := w.AddNode(Node{
		Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddNode(workflow): %v", err)
	}
	if err := w.AddEdge(Edge{
		Key: "e1", SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", FilePath: "ci.yml", StartLine: 1,
		RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v3', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if item.UsedByCount != 0 || len(item.UsedBy) != 0 {
		t.Errorf("v3 borrowed v4's users: %d %v", item.UsedByCount, item.UsedBy)
	}
}
