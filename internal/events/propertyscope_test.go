package events

import (
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// In an account a property scope decides, a repository not owned yet is exactly the one an event
// may be bringing in, so the event is read rather than settled as outside the scope.
func TestAnEventThatCouldBringARepositoryIntoAPropertyScopeIsRead(t *testing.T) {
	f := newFixture(t)
	if err := f.store.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true, IncludesOnly: true,
			Properties: []store.PropertyClause{{Terms: []store.PropertyTerm{{Name: "team", Value: "red"}}}}},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	var a asked
	p := a.hook(f.processor())
	f.deliver("v-1", "custom_property_values", propertyValuesPayload("acme/newcomer", "acme"))
	f.deliver("r-1", "repository", repositoryPayload("acme/fresh", "created"))
	f.deliver("v-2", "custom_property_values", propertyValuesPayload("other/thing", "other"))
	drainWith(t, p)

	f.settled("v-1", store.EventDone, ReasonReadRequested)
	f.settled("r-1", store.EventDone, ReasonReadRequested)
	f.settled("v-2", store.EventIgnored, ReasonNotOwned)
	if !slices.Equal(a.calls, []string{"read acme/newcomer", "read acme/fresh"}) {
		t.Errorf("asked %v, want acme/newcomer and acme/fresh read", a.calls)
	}
}
