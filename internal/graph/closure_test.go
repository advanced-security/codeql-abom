package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The boundary the corpus needed: 204 repositories were analyzed, 0 of them owned, and the one
// declared thing in 56,830 nodes sat unanalyzed at depth 2 behind somebody else's supply chain.
func TestTheClosureStartsAtWhatIsDeclared(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")

	c := closureOf(t, s)

	if !c.Contains("octo-org/app") || !c.Contains("octo-org/tool") {
		t.Error("a declared account's repositories are the roots")
	}
	if c.Roots() == 0 {
		t.Error("Roots is how a caller tells an empty closure from an undeclared one")
	}
}

// A dependency of something you own is your supply chain however deep it sits.
func TestTheClosureFollowsDependenciesOutward(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")

	c := closureOf(t, s)

	if !c.Contains("actions/checkout") {
		t.Error("octo-org/tool names actions/checkout, so it is inside the closure")
	}
}

// The whole point. A repository nothing owned reaches is somebody else's, and following it is how
// the graph grew to 56,830 nodes from a seed nobody owns.
func TestNothingUnreachableFromTheDeclaredSetIsAdmitted(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")

	c := closureOf(t, s)

	if c.Contains("stranger/isolated") {
		t.Error("no owned repository reaches stranger/isolated")
	}
	if c.Contains("stranger/only-they-use-this") {
		t.Error("a dependency reached only from an unowned repository stays outside")
	}
}

// With nothing declared the closure is empty, not universal. Admitting everything would make the
// boundary inert exactly when it has never been configured, which is the state a first run is in.
func TestAnUndeclaredScopeAdmitsNothing(t *testing.T) {
	s := closureFixture(t)

	c := closureOf(t, s)

	if c.Size() != 0 || c.Roots() != 0 {
		t.Errorf("size %d, roots %d; nothing is declared so nothing is in scope",
			c.Size(), c.Roots())
	}
	if c.Contains("octo-org/app") {
		t.Error("an undeclared corpus admits nothing")
	}
}

// Upstream repositories contribute edges, so a route through one is still a route: your action
// depends on theirs, which depends on a third, and all three are your supply chain.
func TestARouteThroughAnUpstreamRepositoryStillReaches(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")

	c := closureOf(t, s)

	if !c.Contains("octo-org/shared") {
		t.Fatal("octo-org/shared is under a declared account")
	}
	if !c.Contains("far/away") {
		t.Error("octo-org/shared reaches far/away, so it is in the supply chain")
	}
}

// Ordering edges are not dependencies, and admitting through one would put a repository in scope
// because two jobs run in sequence.
func TestOrderingEdgesDoNotAdmitAnything(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")

	c := closureOf(t, s)

	if c.Contains("ordered/after") {
		t.Error("job-needs-job is ordering, not dependency; DependencyEdgeFilter excludes it")
	}
}

func closureOf(t *testing.T, s *store.Store) *Closure {
	t.Helper()
	c, err := (&Builder{Store: s}).OwnedClosure()
	if err != nil {
		t.Fatalf("OwnedClosure: %v", err)
	}
	return c
}

func declareOwned(t *testing.T, s *store.Store, owners ...string) {
	t.Helper()
	accounts := make([]store.DeclaredAccount, 0, len(owners))
	for _, o := range owners {
		accounts = append(accounts, store.DeclaredAccount{
			Owner: o, Kind: store.AccountOrganization, Enabled: true,
		})
	}
	if err := s.SetOwnedScope(accounts, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

// GitHub names are case-insensitive, and a `uses:` can spell a repository differently from the
// row it resolves to. The walk keyed both by their exact spelling, so it stopped at the first
// difference, and everything beyond it read as outside the closure — where a prune takes it.
func TestADependencySpelledInAnotherCaseIsStillReachedAndKept(t *testing.T) {
	s := closureFixture(t)
	declareOwned(t, s, "octo-org")
	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	upstream, err := s.UpsertRepository("Up-Stream/Lib", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "octo-org/app/.github/workflows/spelled.yml", Kind: "workflow", RepositoryID: app},
		{Key: "up-stream/lib", Kind: "action", Owner: "up-stream", Repo: "lib", IsExternal: true},
		{Key: "Up-Stream/Lib/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: upstream},
		{Key: "deep/dep", Kind: "action", Owner: "deep", Repo: "dep", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	for _, e := range []store.Edge{
		{Key: "spelled-1", SrcKey: "octo-org/app/.github/workflows/spelled.yml", DstKey: "up-stream/lib",
			Kind: "step-uses-action", RepositoryID: app, FilePath: ".github/workflows/spelled.yml", StartLine: 1},
		{Key: "spelled-2", SrcKey: "Up-Stream/Lib/.github/workflows/ci.yml", DstKey: "deep/dep",
			Kind: "step-uses-action", RepositoryID: upstream, FilePath: ".github/workflows/ci.yml", StartLine: 1},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	c := closureOf(t, s)
	if !c.Contains("Up-Stream/Lib") {
		t.Error("the repository a dependency names in another case is outside the closure")
	}
	if !c.Contains("deep/dep") {
		t.Error("the walk stopped at a repository spelled in another case")
	}
	keep, _, err := (&Builder{Store: s}).KeepList()
	if err != nil {
		t.Fatalf("KeepList: %v", err)
	}
	found := false
	for _, name := range keep {
		found = found || name == "Up-Stream/Lib"
	}
	if !found {
		t.Errorf("the kept list %v drops Up-Stream/Lib, so a prune would take what it holds", keep)
	}
}
