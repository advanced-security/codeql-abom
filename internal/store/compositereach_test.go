package store

import (
	"path/filepath"
	"testing"
)

// The claim only ABOM can make: an allow-list is checked against the `uses:` a workflow wrote, and
// nothing checks what that action goes on to run. Detecting it needs the closure, so the traversal
// is the load-bearing half of the rule.
//
// The composite's own steps are recorded against the repository that holds them, and `owner` is
// empty on a step node while `repo` carries the whole full name — so the join has to go through
// `repository_row_id`. Getting that wrong reports zero on a corpus with 4,970 real instances.
func TestAnActionReachedThroughACompositeIsFound(t *testing.T) {
	s := compositeFixture(t)

	reached, err := s.ActionsReachedThroughComposites()
	if err != nil {
		t.Fatalf("ActionsReachedThroughComposites: %v", err)
	}

	var found *ReachedAction
	for i := range reached {
		if reached[i].Reached == "stranger/exfiltrate" {
			found = &reached[i]
		}
	}
	if found == nil {
		t.Fatalf("the action reached through the composite is missing from %d results", len(reached))
	}
	if found.Repository != "octo-org/app" {
		t.Errorf("Repository = %q, want the repository whose workflow named the composite",
			found.Repository)
	}
	if found.Through != "vendor/toolkit" {
		t.Errorf("Through = %q, want the action the workflow actually wrote", found.Through)
	}
	// The operator can only act at the `uses:` they wrote, so that is where the finding belongs.
	if found.FilePath != ".github/workflows/ci.yml" || found.StartLine != 12 {
		t.Errorf("location = %s:%d, want the top-level uses site", found.FilePath, found.StartLine)
	}
	if found.Depth != 1 {
		t.Errorf("Depth = %d, want 1 hop inside the composite", found.Depth)
	}
}

// One hop would miss the case the rule is for: a chain long enough that nobody reviewing the
// workflow would find it by reading.
func TestReachFollowsMoreThanOneCompositeHop(t *testing.T) {
	s := compositeFixture(t)

	reached, err := s.ActionsReachedThroughComposites()
	if err != nil {
		t.Fatalf("ActionsReachedThroughComposites: %v", err)
	}

	for _, r := range reached {
		if r.Reached == "stranger/deeper" {
			if r.Depth != 2 {
				t.Errorf("Depth = %d, want 2", r.Depth)
			}
			if r.Through != "vendor/toolkit" {
				t.Errorf("Through = %q, want the action the workflow named, not the one in between",
					r.Through)
			}
			return
		}
	}
	t.Error("an action two composites deep was not reached")
}

// A composite that uses itself, directly or around a ring, must not spin. Real corpora contain
// them, and a traversal that hangs on one is a traversal nobody can run.
func TestAReferenceCycleTerminates(t *testing.T) {
	s := compositeFixture(t)

	reached, err := s.ActionsReachedThroughComposites()
	if err != nil {
		t.Fatalf("ActionsReachedThroughComposites: %v", err)
	}
	for _, r := range reached {
		if r.Depth > maxCompositeDepth {
			t.Fatalf("%s reached at depth %d, past the bound", r.Reached, r.Depth)
		}
	}
}

// An action a workflow names directly is checked by GitHub's own allow-list, so reporting it here
// would duplicate the platform and bury the reachable ones nobody else can see.
func TestADirectlyNamedActionIsNotReportedAsReached(t *testing.T) {
	s := compositeFixture(t)

	reached, err := s.ActionsReachedThroughComposites()
	if err != nil {
		t.Fatalf("ActionsReachedThroughComposites: %v", err)
	}
	for _, r := range reached {
		if r.Reached == "vendor/toolkit" {
			t.Error("the action the workflow named directly is not something it reaches indirectly")
		}
	}
}

// A repository holds many composites and a reference names one. Following every composite in the
// repository reported a consumer as reaching an action that only that repository's own CI runs —
// 666 corpus instances rested on it, and the headline example was one of these.
func TestOnlyTheCompositeTheCallerNamedIsFollowed(t *testing.T) {
	s := compositeFixture(t)

	reached, err := s.ActionsReachedThroughComposites()
	if err != nil {
		t.Fatalf("ActionsReachedThroughComposites: %v", err)
	}
	for _, r := range reached {
		if r.Reached == "stranger/ci-only" && r.Through == "vendor/toolkit" {
			t.Errorf("%s was reported as reaching an action used only by %s's own CI helper",
				r.Repository, r.Through)
		}
	}
	// And the narrowing is by subpath, not because the edge is missing: naming that composite
	// directly does reach it.
	var named bool
	for _, r := range reached {
		if r.Through == "vendor/toolkit/.github/actions/ci" && r.Reached == "stranger/ci-only" {
			named = true
		}
	}
	if !named {
		t.Error("a workflow naming the CI composite directly must still reach what it runs")
	}
}

func compositeFixture(t *testing.T) *Store {
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
	toolkit, err := s.UpsertRepository("vendor/toolkit", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	middle, err := s.UpsertRepository("vendor/middle", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	ring, err := s.UpsertRepository("vendor/ring", "")
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
	add(Node{Key: "vendor/toolkit", Kind: "action", Owner: "vendor", Repo: "toolkit",
		IsExternal: true})
	add(Node{Key: "vendor/middle", Kind: "action", Owner: "vendor", Repo: "middle",
		IsExternal: true})
	add(Node{Key: "vendor/ring", Kind: "action", Owner: "vendor", Repo: "ring", IsExternal: true})
	add(Node{Key: "stranger/exfiltrate", Kind: "action", Owner: "stranger", Repo: "exfiltrate",
		IsExternal: true})
	add(Node{Key: "stranger/deeper", Kind: "action", Owner: "stranger", Repo: "deeper",
		IsExternal: true})
	// Step nodes as the extractor writes them: no owner, the whole full name in repo, and the
	// path of the action.yml that holds the step. That path is what says which composite a caller
	// reaches, since a repository can hold many and a reference names one.
	add(Node{Key: "step:vendor/toolkit:action.yml::0", Kind: "step", Repo: "vendor/toolkit",
		Path: "action.yml", RepositoryID: toolkit})
	add(Node{Key: "step:vendor/toolkit:action.yml::1", Kind: "step", Repo: "vendor/toolkit",
		Path: "action.yml", RepositoryID: toolkit})
	add(Node{Key: "step:vendor/middle:action.yml::0", Kind: "step", Repo: "vendor/middle",
		Path: "action.yml", RepositoryID: middle})
	add(Node{Key: "step:vendor/ring:action.yml::0", Kind: "step", Repo: "vendor/ring",
		Path: "action.yml", RepositoryID: ring})
	// The same repository's own CI helper. Nothing a consumer writes reaches it.
	add(Node{Key: "step:vendor/toolkit:.github/actions/ci/action.yml::0", Kind: "step",
		Repo: "vendor/toolkit", Path: ".github/actions/ci/action.yml", RepositoryID: toolkit})
	add(Node{Key: "stranger/ci-only", Kind: "action", Owner: "stranger", Repo: "ci-only",
		IsExternal: true})
	add(Node{Key: "vendor/toolkit/.github/actions/ci", Kind: "action", Owner: "vendor",
		Repo: "toolkit", Path: ".github/actions/ci", IsExternal: true})

	edges := []Edge{
		{Key: "top", SrcKey: "octo-org/app/.github/workflows/ci.yml", DstKey: "vendor/toolkit",
			Kind: "step-uses-action", RepositoryID: app,
			FilePath: ".github/workflows/ci.yml", StartLine: 12},
		{Key: "inner", SrcKey: "step:vendor/toolkit:action.yml::0", DstKey: "stranger/exfiltrate",
			Kind: "composite-step-uses-action", RepositoryID: toolkit,
			FilePath: "action.yml", StartLine: 5},
		{Key: "chain", SrcKey: "step:vendor/toolkit:action.yml::1", DstKey: "vendor/middle",
			Kind: "composite-step-uses-action", RepositoryID: toolkit,
			FilePath: "action.yml", StartLine: 9},
		{Key: "deep", SrcKey: "step:vendor/middle:action.yml::0", DstKey: "stranger/deeper",
			Kind: "composite-step-uses-action", RepositoryID: middle,
			FilePath: "action.yml", StartLine: 3},
		// A ring: toolkit's composite reaches ring, whose composite reaches toolkit again.
		{Key: "ring-in", SrcKey: "step:vendor/middle:action.yml::0", DstKey: "vendor/ring",
			Kind: "composite-step-uses-action", RepositoryID: middle,
			FilePath: "action.yml", StartLine: 7},
		{Key: "ring-out", SrcKey: "step:vendor/ring:action.yml::0", DstKey: "vendor/toolkit",
			Kind: "composite-step-uses-action", RepositoryID: ring,
			FilePath: "action.yml", StartLine: 4},
		{Key: "ci-only", SrcKey: "step:vendor/toolkit:.github/actions/ci/action.yml::0",
			DstKey: "stranger/ci-only", Kind: "composite-step-uses-action", RepositoryID: toolkit,
			FilePath: ".github/actions/ci/action.yml", StartLine: 2},
		{Key: "names-ci", SrcKey: "octo-org/app/.github/workflows/ci.yml",
			DstKey: "vendor/toolkit/.github/actions/ci", Kind: "step-uses-action", RepositoryID: app,
			FilePath: ".github/workflows/ci.yml", StartLine: 20},
	}
	for _, e := range edges {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}
