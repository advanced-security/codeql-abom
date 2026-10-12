package serve

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func readUnits(t *testing.T, srv *Server, query string) propertiesView {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest("GET", "/api/properties"+query, nil))
	var out propertiesView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return out
}

func withUnits(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st := newServer(t)
	declare(t, st, "has-ghas")
	srv.GroupProperty = "business_id"
	for repo, unit := range map[string]string{
		"has-ghas/a": "3", "has-ghas/b": "3", "has-ghas/c": "1",
	} {
		if _, err := st.InsertOwnedRepository(repo); err != nil {
			t.Fatal(err)
		}
		if err := st.ReplacePropertyValues(repo, []store.PropertyValue{
			{Name: "business_id", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.InsertOwnedRepository("has-ghas/d"); err != nil {
		t.Fatal(err)
	}
	return srv, st
}

// The parts have to reconcile with a whole that exists independently of them. A breakdown that only
// counts what it found cannot tell a reader what it missed.
func TestTheUnitBreakdownReconcilesWithTheOwnedTotal(t *testing.T) {
	srv, _ := withUnits(t)

	got := readUnits(t, srv, "")

	if got.Breakdown.Repositories != 4 {
		t.Errorf("Repositories = %d, want all four declared", got.Breakdown.Repositories)
	}
	if got.Breakdown.WithValue+got.Breakdown.WithoutValue != got.Breakdown.Repositories {
		t.Errorf("with=%d without=%d do not sum to %d",
			got.Breakdown.WithValue, got.Breakdown.WithoutValue, got.Breakdown.Repositories)
	}
	if got.Breakdown.WithoutValue != 1 {
		t.Errorf("WithoutValue = %d, want the repository the organization never placed",
			got.Breakdown.WithoutValue)
	}
}

// A business unit is read, never inferred, so the key an operator configured is reported back. A
// reader shown "3 repositories in unit 3" and not told which property that came from cannot check
// it against the organization.
func TestTheConfiguredKeyIsNamedInTheAnswer(t *testing.T) {
	srv, _ := withUnits(t)

	got := readUnits(t, srv, "")

	if got.Configured != "business_id" {
		t.Errorf("Configured = %q, want the key the operator named", got.Configured)
	}
	if got.Breakdown.Name != "business_id" {
		t.Errorf("Breakdown.Name = %q, want the key it was counted for", got.Breakdown.Name)
	}
}

// The keys the organization actually administers are reported, so an operator configuring one is
// shown what exists rather than asked to remember it.
func TestTheKeysTheOrganizationAdministersAreOffered(t *testing.T) {
	srv, st := withUnits(t)
	if err := st.ReplacePropertyValues("has-ghas/d", []store.PropertyValue{
		{Name: "phi-scan", Value: "scan-pending"}}); err != nil {
		t.Fatal(err)
	}

	got := readUnits(t, srv, "")

	if len(got.Keys) != 2 || got.Keys[0] != "business_id" || got.Keys[1] != "phi-scan" {
		t.Errorf("Keys = %v, want both keys the corpus has seen", got.Keys)
	}
}

// A key no repository sets is its own state and is reported, never silently dropped: it is how an
// operator finds out they configured a key the organization does not administer.
func TestAKeyTheOrganizationDoesNotAdministerIsStillAnswered(t *testing.T) {
	srv, _ := withUnits(t)

	got := readUnits(t, srv, "?property=app_name")

	if got.Breakdown.Name != "app_name" {
		t.Errorf("Breakdown.Name = %q, want the key that was asked about", got.Breakdown.Name)
	}
	if len(got.Breakdown.Values) != 0 {
		t.Errorf("Values = %+v, want none", got.Breakdown.Values)
	}
	if got.Breakdown.WithoutValue != 4 {
		t.Errorf("WithoutValue = %d, want every repository reported as unplaced",
			got.Breakdown.WithoutValue)
	}
}

// With no key configured there is no dividing line, and inventing one from whatever the corpus
// happens to hold would be exactly the inference this plan forbids.
func TestWithNoKeyConfiguredNothingIsGuessed(t *testing.T) {
	srv, _ := newServer(t)

	got := readUnits(t, srv, "")

	if got.Configured != "" {
		t.Errorf("Configured = %q, want nothing claimed", got.Configured)
	}
	if got.Breakdown.Name != "" || len(got.Breakdown.Values) != 0 {
		t.Errorf("Breakdown = %+v, want no grouping until somebody names the key", got.Breakdown)
	}
}
