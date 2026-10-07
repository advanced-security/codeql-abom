package enumerate

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// scopedStore declares has-ghas as the repositories carrying team=a, a property only organization
// actors can set.
func scopedStore(t *testing.T) *store.Store {
	t.Helper()
	s := newStore(t)
	if err := s.ReplacePropertySchema("has-ghas", []store.PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: store.PropertyEditableByOrganization},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "has-ghas",
		Kind: store.AccountOrganization, Enabled: true, IncludesOnly: true,
		Properties: []store.PropertyClause{{Terms: []store.PropertyTerm{{Name: "team", Value: "a"}}}},
	}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	return s
}

func team(name, value string) catalog.Repository {
	return withProperties(name, catalog.PropertyValue{Name: "team", Value: value})
}

func owned(t *testing.T, s *store.Store) string {
	t.Helper()
	names, err := s.OwnedRepositories()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// The listing is GitHub's answer about each repository, so it is what decides which ones a
// property scope takes; nothing the listing named outside the scope is recorded as the operator's.
func TestAPropertyScopeTakesTheRepositoriesTheListingSaysCarryIt(t *testing.T) {
	s := scopedStore(t)
	r := &reader{listings: []catalog.Listing{{Route: catalog.RouteGraphQL, Full: true,
		PropertiesRead: true, Repositories: []catalog.Repository{
			team("has-ghas/one", "a"), team("has-ghas/two", "b"), {FullName: "has-ghas/three"}}}}}

	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}
	if got := owned(t, s); got != "has-ghas/one" {
		t.Errorf("owned %q, want only has-ghas/one", got)
	}
	if report.Accounts[0].Added != 1 {
		t.Errorf("added %d, want the one repository the scope covers", report.Accounts[0].Added)
	}
	if held, _ := s.PropertyValues("has-ghas/two"); len(held) != 0 {
		t.Errorf("kept %v for has-ghas/two, which nothing declares", held)
	}
}

// A repository retagged out of the scope leaves it on the read that shows the change, and takes
// its values with it. It still exists, so it is not reported missing.
func TestARepositoryThatStopsMatchingLeavesWithItsValues(t *testing.T) {
	s := scopedStore(t)
	r := &reader{listings: []catalog.Listing{
		{Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true,
			Repositories: []catalog.Repository{team("has-ghas/one", "a")}},
		{Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true,
			Repositories: []catalog.Repository{team("has-ghas/one", "b")}},
	}}
	var report Report
	for range 2 {
		var err error
		if report, err = Run(context.Background(), Options{Store: s, Read: r.read}); err != nil {
			t.Fatal(err)
		}
	}

	if got := owned(t, s); got != "" {
		t.Errorf("owned %q after has-ghas/one was retagged", got)
	}
	if got := report.Accounts[0].Left; got != 1 {
		t.Errorf("the read that let it go reports %d left, want 1", got)
	}
	if held, _ := s.PropertyValues("has-ghas/one"); len(held) != 0 {
		t.Errorf("kept %v for a repository no longer covered", held)
	}
	marks, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(marks.Missing) != 0 {
		t.Errorf("reported %v missing; it was retagged, not deleted", marks.Missing)
	}
}

// A read that could not carry the values cannot judge a property scope, so it moves nothing, and
// says how many it could not judge rather than reading them as non-matching.
func TestAListingThatCouldNotReadPropertiesChangesNoOwnership(t *testing.T) {
	s := scopedStore(t)
	r := &reader{listings: []catalog.Listing{
		{Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true,
			Repositories: []catalog.Repository{team("has-ghas/one", "a")}},
		{Route: catalog.RouteREST, Full: true, PropertiesRead: false,
			Repositories: []catalog.Repository{{FullName: "has-ghas/one"}, {FullName: "has-ghas/two"}}},
	}}
	if _, err := Run(context.Background(), Options{Store: s, Read: r.read}); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}

	if got := owned(t, s); got != "has-ghas/one" {
		t.Errorf("owned %q after a read without values, want has-ghas/one kept", got)
	}
	if got := report.Accounts[0].PropertiesUnjudged; got != 2 {
		t.Errorf("reported %d not judged, want both repositories the read named", got)
	}
	marks, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(marks.Missing) != 0 {
		t.Errorf("reported %v missing; a read that could not judge it is not a deletion", marks.Missing)
	}
	if marks.PropertiesJudgedAt.IsZero() || marks.PropertiesUnjudged != 2 {
		t.Errorf("recorded %+v, want the full listing's 2 not judged on record", marks)
	}
}

// Only a full listing can find every repository a changed scope newly covers; a sweep reads the
// repositories that moved, and retagging moves nothing it walks by.
func TestAPropertyScopeNewerThanTheLastFullListingIsReadWhole(t *testing.T) {
	s := scopedStore(t)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 5, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	r := &reader{}
	if _, err := Run(context.Background(), Options{Store: s, Read: r.read}); err != nil {
		t.Fatal(err)
	}
	if len(r.asked) != 1 || r.asked[0].Empty() {
		t.Fatalf("asked %+v, want a sweep while the scope is older than the listing", r.asked)
	}

	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "has-ghas",
		Kind: store.AccountOrganization, Enabled: true, IncludesOnly: true,
		Properties: []store.PropertyClause{{Terms: []store.PropertyTerm{{Name: "team", Value: "b"}}}},
	}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{Store: s, Read: r.read}); err != nil {
		t.Fatal(err)
	}
	if len(r.asked) != 2 || !r.asked[1].Empty() {
		t.Errorf("asked %+v, want the account read whole after its scope changed", r.asked)
	}
}

// An event names a repository that may be entering the scope, so it is read whatever its
// ownership is now; reading only what is already owned would never let anything in.
func TestARepositoryAnEventNamesIsReadWhenItsAccountHoldsAPropertyScope(t *testing.T) {
	s := scopedStore(t)
	var asked []string
	read := func(_ context.Context, fullName string) (catalog.Listing, error) {
		asked = append(asked, fullName)
		return catalog.Listing{Route: catalog.RouteREST, PropertiesRead: true,
			Repositories: []catalog.Repository{team(fullName, "a")}}, nil
	}

	if _, err := Run(context.Background(), Options{Store: s, Read: (&reader{}).read,
		Repositories: []string{"has-ghas/new"}, ReadRepository: read}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %v, want has-ghas/new read", asked)
	}
	if got := owned(t, s); got != "has-ghas/new" {
		t.Errorf("owned %q, want has-ghas/new taken in", got)
	}
}

// A scope saved before the account's schema was ever read is held until a read says who may set
// the property, and that read is this one: the operator who just pressed Save sees it take effect.
func TestAScopeSavedBeforeTheSchemaWasReadTakesRepositoriesOnTheFirstRead(t *testing.T) {
	s := newStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "has-ghas",
		Kind: store.AccountOrganization, Enabled: true, IncludesOnly: true,
		Properties: []store.PropertyClause{{Terms: []store.PropertyTerm{{Name: "team", Value: "a"}}}},
	}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	schema := func(context.Context, string) ([]catalog.PropertyDefinition, error) {
		return []catalog.PropertyDefinition{{Name: "team", ValueType: "single_select",
			ValuesEditableBy: store.PropertyEditableByOrganization}}, nil
	}
	r := &reader{listings: []catalog.Listing{{Route: catalog.RouteGraphQL, Full: true,
		PropertiesRead: true, Repositories: []catalog.Repository{team("has-ghas/one", "a")}}}}

	if _, err := Run(context.Background(), Options{Store: s, Read: r.read, Schema: schema}); err != nil {
		t.Fatal(err)
	}
	if got := owned(t, s); got != "has-ghas/one" {
		t.Errorf("owned %q after the first read, want has-ghas/one", got)
	}
}

// A multi-select's default reaches the store, so a scope naming one of its values says it takes in
// every repository nobody set.
func TestAMultiSelectDefaultIsRecordedWithTheSchema(t *testing.T) {
	s := newStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "has-ghas",
		Kind: store.AccountOrganization, Enabled: true, IncludesOnly: true,
		Properties: []store.PropertyClause{{Terms: []store.PropertyTerm{{Name: "tags", Value: "a"}}}},
	}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	schema := func(context.Context, string) ([]catalog.PropertyDefinition, error) {
		return []catalog.PropertyDefinition{{Name: "tags", ValueType: "multi_select",
			ValuesEditableBy: store.PropertyEditableByOrganization, DefaultValues: []string{"a", "b"}}}, nil
	}
	r := &reader{listings: []catalog.Listing{{Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true}}}

	if _, err := Run(context.Background(), Options{Store: s, Read: r.read, Schema: schema}); err != nil {
		t.Fatal(err)
	}
	declared, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if got := declared[0].Properties[0].Defaults; len(got) != 1 || got[0] != "tags" {
		t.Errorf("the scope names the default of %v, want tags", got)
	}
}
