package store

import (
	"path/filepath"
	"testing"
)

// unitGraphFixture holds one owned repository carrying a unit, one upstream repository that carries
// nothing, and an external action neither of them owns.
//
//	octo-org/app   business_id=3   owned
//	other/lib                      upstream, analyzed only to resolve what app depends on
//	actions/checkout               external, belongs to nobody
func unitGraphFixture(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	lib, err := s.InsertUpstreamRepository("other/lib")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	if err := s.ReplacePropertyValues("octo-org/app", []PropertyValue{
		{Name: "business_id", Value: "3"},
	}); err != nil {
		t.Fatalf("ReplacePropertyValues: %v", err)
	}
	// The upstream repository carries a value too. Nothing should read it: a unit divides what the
	// operator owns, and a value on somebody else's repository is not theirs to be grouped by.
	if err := s.ReplacePropertyValues("other/lib", []PropertyValue{
		{Name: "business_id", Value: "9"},
	}); err != nil {
		t.Fatalf("ReplacePropertyValues: %v", err)
	}

	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app,
		Path: ".github/workflows/ci.yml"})
	add(Node{Key: "other/lib/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: lib,
		Path: ".github/workflows/ci.yml"})
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})
	add(Node{Key: "app-step", Kind: "step", RepositoryID: app,
		ContainerKey: "octo-org/app/.github/workflows/ci.yml"})
	if err := w.AddEdge(Edge{
		Key: "app->checkout", SrcKey: "app-step", DstKey: "actions/checkout",
		Kind: "step-uses-action", FilePath: ".github/workflows/ci.yml", StartLine: 1,
		RepositoryID: app,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

func unitsByNode(t *testing.T, s *Store, property string) map[string]string {
	t.Helper()
	g, err := s.FullGraph(FullGraphOptions{IncludeSteps: true, GroupProperty: property})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	out := map[string]string{}
	for _, n := range g.Nodes {
		out[n.Key] = n.Grouping
	}
	return out
}

// Grouping the graph by business unit needs the unit on the node, because grouping happens in the
// browser from what each node carries. Everything the operator owns carries the unit of the
// repository it lives in — a workflow in their repository is theirs, not a dependency.
func TestANodeInAnOwnedRepositoryCarriesThatRepositorysUnit(t *testing.T) {
	s := unitGraphFixture(t)

	units := unitsByNode(t, s, "business_id")
	for _, key := range []string{"octo-org/app/.github/workflows/ci.yml", "app-step"} {
		if units[key] != "3" {
			t.Errorf("%s carries unit %q, want \"3\": it lives in a repository the operator owns",
				key, units[key])
		}
	}
}

// A dependency belongs to whoever wrote it, not to whoever reached it. Inheriting a unit down an
// edge would place somebody else's action in a business unit that never chose it, and the whole
// point of reading properties from the organization is that a placement is never guessed.
func TestADependencyNeverInheritsTheUnitOfWhateverReachedIt(t *testing.T) {
	s := unitGraphFixture(t)

	units := unitsByNode(t, s, "business_id")
	if got := units["actions/checkout"]; got != "" {
		t.Errorf("actions/checkout carries unit %q; an external action belongs to no unit", got)
	}
}

// A repository outside the owned organization is somebody else's, whatever ABOM fetched it for.
// Properties are only ever collected for the organization the operator declared — there is usually
// no permission to read anybody else's — so a value found on one must not become a dividing line
// the operator does not administer.
func TestARepositoryOutsideTheOwnedOrganizationCarriesNoUnit(t *testing.T) {
	s := unitGraphFixture(t)

	units := unitsByNode(t, s, "business_id")
	if got := units["other/lib/.github/workflows/ci.yml"]; got != "" {
		t.Errorf("an upstream repository's node carries unit %q, want none", got)
	}
}

// With no key configured there is no dividing line, and a node claiming one would invent it.
func TestWithNoConfiguredKeyNoNodeCarriesAUnit(t *testing.T) {
	s := unitGraphFixture(t)

	for key, unit := range unitsByNode(t, s, "") {
		if unit != "" {
			t.Errorf("%s carries unit %q with no property configured", key, unit)
		}
	}
}
