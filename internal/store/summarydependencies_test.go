package store

import "testing"

// The Dashboard prints this figure under the word "actions", and the graph is mostly not actions.
// Counting every node overstates what the operator depends on.
//
// The kinds are the Inventory's, because the card links there and two definitions of a dependency
// would have the page disagree with the screen it opens.
func TestSummaryCountsDependenciesApartFromEveryNode(t *testing.T) {
	s := advisoryStore(t)
	repo, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repo},
		{Key: "job:build", Kind: "job", RepositoryID: repo},
		{Key: "step:0", Kind: "step", RepositoryID: repo},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout"},
		{Key: "octo-org/shared", Kind: "composite_action", Owner: "octo-org", Repo: "shared"},
		{Key: "docker://alpine:3.20", Kind: "docker_image"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Nodes != 6 {
		t.Errorf("Nodes = %d, want every node the graph holds", got.Nodes)
	}
	if got.Dependencies != 3 {
		t.Errorf("Dependencies = %d, want the action, the composite action and the image",
			got.Dependencies)
	}
}
