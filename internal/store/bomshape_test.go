package store

import (
	"testing"
)

// A step is a position in a file, not a thing you depend on. Left in, they were 10,530 of 11,290
// components on the reference corpus — 93% of a document that is supposed to answer "what is this
// composed of".
func TestStepsAndJobsAreNotComponents(t *testing.T) {
	doc := build(t, stepFindingFixture(t), BOMOptions{})

	var steps int
	for _, c := range doc.Components {
		if c.Kind == "step" || c.Kind == "job" {
			t.Errorf("%s is a %s and is not something anything depends on", c.Key, c.Kind)
		}
		steps++
	}
	if steps == 0 {
		t.Fatal("the document is empty, so this proves nothing")
	}
}

// Folding a step away must not drop the evidence recorded against it: on the reference corpus
// 1,223 components carrying findings were steps.
func TestAFindingOnAStepBecomesTheContainersFinding(t *testing.T) {
	s := stepFindingFixture(t)
	doc := build(t, s, BOMOptions{})

	c := componentOf(doc, "octo-org/app/.github/workflows/ci.yml")
	if c == nil {
		t.Fatal("the workflow holding the step is not a component")
	}
	if len(c.Findings) == 0 {
		t.Error("the step's finding was lost when the step was folded away")
	}
	if doc.Coverage.Findings == 0 {
		t.Error("coverage does not count a finding the document still contains")
	}
}

func stepFindingFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app,
			Path: ".github/workflows/ci.yml"},
		{Key: "step:app:0", Kind: "step", RepositoryID: app,
			ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "e1", SrcKey: "step:app:0", DstKey: "actions/checkout", Kind: "step-uses-action",
		RefRaw: "actions/checkout@v4", RefValue: "v4", RepositoryID: app,
		FilePath: ".github/workflows/ci.yml", StartLine: 10,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: app,
		FilePath: ".github/workflows/ci.yml", StartLine: 10, Fingerprint: "f1",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
	return s
}
