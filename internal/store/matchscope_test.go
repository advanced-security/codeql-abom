package store

import "testing"

// Scoping reads the repository the finding was *found in*, never the one the node belongs to.
//
// Found in the browser, not here: on the reference corpus **93 of the 179 nodes matching
// `actions/code-injection/*` have no repository row at all** — they are steps inside external
// composite actions — and the finding names a repository in every one of those 93. Scoping on the
// node would have silently dropped 52% of a gate's answer, which is the direction that lets a pull
// request pass because the check quietly did not see anything.
func TestScopingReadsWhereTheFindingWasFoundNotWhereTheNodeLives(t *testing.T) {
	s := matchFixture(t)
	var toolID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, "octo-org/tool").
		Scan(&toolID); err != nil {
		t.Fatalf("finding octo-org/tool: %v", err)
	}

	// A step inside an external composite action: it belongs to no repository, but the edge it
	// sources was produced by analyzing one.
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{Key: "actions/checkout", Kind: "action", Owner: "actions",
		Repo: "checkout", IsExternal: true}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddNode(Node{Key: "step:external:0", Kind: "step", Path: "action.yml"}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddEdge(Edge{
		Key: "e-external", SrcKey: "step:external:0", DstKey: "actions/checkout",
		Kind: "composite-step-uses-action", RefValue: "v4", RepositoryID: toolID,
		FilePath: "action.yml", StartLine: 200,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		RuleID: "abom/external-step", RepositoryID: toolID,
		FilePath: "action.yml", StartLine: 200,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var nodeRepo any
	if err := s.DB().QueryRow(
		`SELECT repository_row_id FROM abom_node WHERE node_key = 'step:external:0'`).
		Scan(&nodeRepo); err != nil {
		t.Fatalf("reading the node's repository: %v", err)
	}
	if nodeRepo != nil {
		t.Fatalf("the fixture gave the node a repository (%v); it must have none", nodeRepo)
	}

	got, err := s.NodesMatching(FindingSelector{
		Rules:        []string{"abom/external-step"},
		Repositories: []string{"octo-org/tool"},
	})
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("matched %d nodes, want the step whose finding was found in the scoped repository",
			len(got.Nodes))
	}
	if len(got.Nodes[0].Repositories) != 1 || got.Nodes[0].Repositories[0] != "octo-org/tool" {
		t.Errorf("Repositories = %v, want the repository the finding was found in",
			got.Nodes[0].Repositories)
	}
}

// A gate asks about one pull request's repository, not about the corpus. Without this the policy
// "fail on named view X" would fail every repository whenever any repository matched.
func TestAMatchCanBeScopedToTheRepositoriesAGateIsAskingAbout(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{
		Rules:        []string{"actions/*"},
		Repositories: []string{"octo-org/tool"},
	})

	if len(got) != 1 || got[0] != "octo-org/tool/.github/workflows/ci.yml" {
		t.Errorf("matched %v, want only the repository the gate asked about", got)
	}
}

// Seven accounts in the reference corpus spell themselves both ways, and a case-sensitive compare
// once misclassified 211 edges. A gate handed `Octo-Org/Tool` by an Actions context must get the
// same answer as one handed `octo-org/tool`, or a policy passes because of capitalization.
func TestRepositoryScopingIsCaseInsensitive(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{
		Rules:        []string{"actions/*"},
		Repositories: []string{"Octo-Org/Tool"},
	})

	if len(got) != 1 {
		t.Errorf("matched %v, want the same repository under a different spelling", got)
	}
}

// Naming a repository that has nothing must answer nothing, not everything. This is the direction
// that matters: a gate whose scope failed to match would otherwise fail the pull request on
// somebody else's findings.
func TestScopingToARepositoryWithNoFindingsMatchesNothing(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{
		Rules:        []string{"actions/*"},
		Repositories: []string{"octo-org/nothing-here"},
	})

	if len(got) != 0 {
		t.Errorf("matched %v for a repository with no findings, want none", got)
	}
}
