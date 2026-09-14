package enumerate

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func withProperties(name string, values ...catalog.PropertyValue) catalog.Repository {
	return catalog.Repository{FullName: name, Properties: values}
}

// The dividing line is read from the organization and stored, so that every surface groups by the
// same thing rather than each deriving its own answer from a repository name.
func TestEnumerationStoresWhatTheOrganizationSaysARepositoryIs(t *testing.T) {
	s := newStore(t)
	r := &reader{listings: []catalog.Listing{{
		Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true,
		Repositories: []catalog.Repository{
			withProperties("has-ghas/one", catalog.PropertyValue{Name: "business_id", Value: "3"}),
			withProperties("has-ghas/two", catalog.PropertyValue{Name: "business_id", Value: "1"}),
		},
	}}}

	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accounts[0].PropertiesRead {
		t.Error("the values arrived with the listing and the report says otherwise")
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	if units.WithValue != 2 || len(units.Values) != 2 {
		t.Errorf("breakdown %+v, want both repositories placed", units)
	}
}

// A token that cannot read properties is a permission problem, not an organization that divides its
// repositories into nothing. Writing an empty set would erase a grouping that is still correct.
func TestPropertiesThatWereNotReadDoNotEraseWhatIsStored(t *testing.T) {
	s := newStore(t)
	if err := s.ReplacePropertyValues("has-ghas/one", []store.PropertyValue{
		{Name: "business_id", Value: "3"}}); err != nil {
		t.Fatal(err)
	}

	r := &reader{listings: []catalog.Listing{{
		Route: catalog.RouteREST, Full: true, PropertiesRead: false,
		Repositories: []catalog.Repository{{FullName: "has-ghas/one"}},
	}}}
	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}
	if report.Accounts[0].PropertiesRead {
		t.Error("nothing was read, and the report claims otherwise")
	}

	held, err := s.PropertyValues("has-ghas/one")
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 {
		t.Errorf("stored %v, want the grouping kept when nobody could ask about it", held)
	}
}

// Half the values would read as a deliberate placement in a unit the organization did not choose,
// so a repository whose properties could not be carried whole keeps what it had and is counted.
func TestARepositoryWhosePropertiesWereTruncatedIsCountedNotGuessed(t *testing.T) {
	s := newStore(t)
	r := &reader{listings: []catalog.Listing{{
		Route: catalog.RouteGraphQL, Full: true, PropertiesRead: true,
		Repositories: []catalog.Repository{
			{FullName: "has-ghas/one", PropertiesTruncated: true},
			withProperties("has-ghas/two", catalog.PropertyValue{Name: "business_id", Value: "1"}),
		},
	}}}

	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}
	if report.Accounts[0].PropertiesTruncated != 1 {
		t.Errorf("PropertiesTruncated = %d, want the one repository nobody could place",
			report.Accounts[0].PropertiesTruncated)
	}
	held, err := s.PropertyValues("has-ghas/one")
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Errorf("stored %v for a truncated answer, want nothing invented", held)
	}
}
