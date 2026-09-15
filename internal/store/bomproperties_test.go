package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// unitBOMFixture declares one organization holding two repositories in different business units,
// each depending on an action the other does not, so a document bounded to one unit is
// distinguishable from a document about the whole organization.
//
//	octo-org/payments  business_id=3  ci.yml -> actions/checkout
//	octo-org/web       business_id=7  ci.yml -> actions/setup-node
func unitBOMFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
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

	w := NewWriter(s, 0)
	for _, r := range []struct{ name, unit, action string }{
		{"octo-org/payments", "3", "actions/checkout"},
		{"octo-org/web", "7", "actions/setup-node"},
	} {
		id, err := s.UpsertRepository(r.name, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", r.name, err)
		}
		if err := s.ReplacePropertyValues(r.name, []PropertyValue{
			{Name: "business_id", Value: r.unit},
		}); err != nil {
			t.Fatalf("ReplacePropertyValues(%s): %v", r.name, err)
		}

		workflow := r.name + "/.github/workflows/ci.yml"
		step := "step:" + r.name + ":ci:0"
		owner, repo, _ := strings.Cut(r.action, "/")
		for _, n := range []Node{
			{Key: workflow, Kind: "workflow", RepositoryID: id, Path: ".github/workflows/ci.yml"},
			{Key: r.action, Kind: "action", Owner: owner, Repo: repo, IsExternal: true},
			{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow},
		} {
			if err := w.AddNode(n); err != nil {
				t.Fatalf("AddNode(%s): %v", n.Key, err)
			}
		}
		if err := w.AddEdge(Edge{
			Key: step + "->" + r.action, SrcKey: step, DstKey: r.action,
			Kind: "step-uses-action", RefValue: "v1", FilePath: ".github/workflows/ci.yml",
			StartLine: 1, RepositoryID: id,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", r.name, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

func componentKeys(doc BOM) map[string]bool {
	out := map[string]bool{}
	for _, c := range doc.Components {
		out[c.Key] = true
	}
	return out
}

// A bill of materials bounded to one part of the organization is the document that part's owner
// can act on. The whole-organization document names dependencies belonging to somebody else, and
// an owner reading it cannot tell which lines are theirs.
func TestABillOfMaterialsBoundedToAPropertyCoversOnlyItsRepositories(t *testing.T) {
	s := unitBOMFixture(t)

	whole, err := s.BillOfMaterials(BOMOptions{})
	if err != nil {
		t.Fatalf("BillOfMaterials: %v", err)
	}
	if len(whole.Roots) != 2 {
		t.Fatalf("the organization has %d roots, want 2; the fixture is not what the rest of "+
			"this test assumes", len(whole.Roots))
	}

	one, err := s.BillOfMaterials(BOMOptions{
		Properties: []viewspec.PropertyMatch{{Name: "business_id", Value: "3"}}})
	if err != nil {
		t.Fatalf("BillOfMaterials(business_id:3): %v", err)
	}
	if len(one.Roots) != 1 || one.Roots[0] != "octo-org/payments" {
		t.Errorf("roots = %v, want only octo-org/payments", one.Roots)
	}

	keys := componentKeys(one)
	if !keys["actions/checkout"] {
		t.Errorf("the grouping's own dependency is missing; components were %v", keys)
	}
	if keys["actions/setup-node"] {
		t.Error("a dependency belonging to another grouping reached a document bounded to this one")
	}
}

// A grouping nobody is in and an organization nobody has declared are different states, and the
// message is the only thing that tells them apart. Reporting the first as the second sends a
// reader to declare a scope they already declared.
func TestAPropertyThatMatchesNoRepositoryIsNotReportedAsAnUndeclaredScope(t *testing.T) {
	s := unitBOMFixture(t)

	_, err := s.BillOfMaterials(BOMOptions{
		Properties: []viewspec.PropertyMatch{{Name: "business_id", Value: "404"}}})
	if err == nil {
		t.Fatal("a grouping holding no repositories produced a document rather than saying so")
	}
	if errors.Is(err, ErrNoBOMRoot) {
		t.Errorf("a grouping holding no repositories was reported as an undeclared scope: %v", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the error does not name the grouping that matched nothing: %v", err)
	}
}

// Excluding is how an owner asks for everything that is not somebody else's, without having to
// enumerate what is left.
func TestABillOfMaterialsCanExcludeAGrouping(t *testing.T) {
	s := unitBOMFixture(t)

	got, err := s.BillOfMaterials(BOMOptions{
		NotProperties: []viewspec.PropertyMatch{{Name: "business_id", Value: "3"}}})
	if err != nil {
		t.Fatalf("BillOfMaterials(notProperties business_id:3): %v", err)
	}
	if len(got.Roots) != 1 || got.Roots[0] == "octo-org/payments" {
		t.Errorf("roots = %v, want the one root that is not in business unit 3", got.Roots)
	}
}
