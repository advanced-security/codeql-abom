package store

import (
	"slices"
	"testing"
)

// ownedStore is a corpus with the account declared, because "owned" means inside the declared scope
// and not merely present: a breakdown counts against what the operator said is theirs.
func ownedStore(t *testing.T, repositories ...string) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "has-ghas", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, r := range repositories {
		if _, err := s.InsertOwnedRepository(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// A business unit is read, never inferred. The corpus holds what GitHub holds, so a repository's
// values are whatever the organization administers and nothing a name pattern suggested.
func TestPropertyValuesAreStoredAsReadBack(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplacePropertyValues("has-ghas/juice-shop", []PropertyValue{
		{Name: "business_id", Value: "2"},
		{Name: "phi-scan", Value: "scan-pending"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.PropertyValues("has-ghas/juice-shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("read %v, want both properties", got)
	}
	if got[0].Name != "business_id" || got[0].Value != "2" {
		t.Errorf("first value %+v, want business_id=2", got[0])
	}
}

// A multi-select property matches if any of its values matches, which is a plain join only if each
// value is its own row. Folding them into one comma-joined string makes a value containing a comma
// impossible to tell from two values.
func TestAMultiSelectPropertyKeepsEveryValue(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplacePropertyValues("has-ghas/one", []PropertyValue{
		{Name: "tier", Value: "gold"},
		{Name: "tier", Value: "regulated, audited"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.PropertyValues("has-ghas/one")
	if err != nil {
		t.Fatal(err)
	}
	values := []string{}
	for _, v := range got {
		values = append(values, v.Value)
	}
	slices.Sort(values)
	if !slices.Equal(values, []string{"gold", "regulated, audited"}) {
		t.Errorf("values %v, want both kept, comma and all", values)
	}
}

// Replacing means replacing. A property removed in the organization has to disappear here, or the
// corpus reports a repository in a unit it was taken out of.
func TestAPropertyRemovedUpstreamIsRemovedHere(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplacePropertyValues("has-ghas/one", []PropertyValue{
		{Name: "business_id", Value: "3"},
		{Name: "phi-scan", Value: "scan-pending"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReplacePropertyValues("has-ghas/one", []PropertyValue{
		{Name: "business_id", Value: "3"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.PropertyValues("has-ghas/one")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "business_id" {
		t.Errorf("read %v, want only the property the organization still sets", got)
	}
}

// A repository with no value is its own state, never a default bucket. Folding unset into "other"
// makes a missing property indistinguishable from a deliberate one.
func TestRepositoriesWithNoValueAreCountedSeparately(t *testing.T) {
	s := ownedStore(t, "has-ghas/a", "has-ghas/b", "has-ghas/c", "has-ghas/d")
	for repo, unit := range map[string]string{"has-ghas/a": "3", "has-ghas/b": "3", "has-ghas/c": "1"} {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{{Name: "business_id", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	byValue := map[string]int{}
	for _, u := range units.Values {
		byValue[u.Value] = u.Repositories
	}
	if byValue["3"] != 2 || byValue["1"] != 1 {
		t.Errorf("breakdown %+v, want 3 twice and 1 once", units.Values)
	}
	if units.WithoutValue != 1 {
		t.Errorf("WithoutValue = %d, want the one repository the organization never set",
			units.WithoutValue)
	}
	if _, folded := byValue[""]; folded {
		t.Error("an unset property became a value of its own, which is a default bucket by another name")
	}
}

// Filtering by one unit and summing across all of them, the no-value state included, has to
// reconcile with the unfiltered total. If it does not, something is being dropped rather than
// grouped, and no reader would be able to tell which.
func TestEveryOwnedRepositoryLandsInExactlyOneBucket(t *testing.T) {
	s := ownedStore(t, "has-ghas/a", "has-ghas/b", "has-ghas/c", "has-ghas/d", "has-ghas/e")
	if err := s.ReplacePropertyValues("has-ghas/a", []PropertyValue{
		{Name: "business_id", Value: "3"}}); err != nil {
		t.Fatal(err)
	}
	// Two values on one repository: it belongs to both, so the parts may exceed the whole and the
	// reader has to be able to see that rather than have it silently normalized away.
	if err := s.ReplacePropertyValues("has-ghas/b", []PropertyValue{
		{Name: "business_id", Value: "1"}, {Name: "business_id", Value: "2"}}); err != nil {
		t.Fatal(err)
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	if units.Repositories != 5 {
		t.Errorf("Repositories = %d, want every owned repository counted once", units.Repositories)
	}
	if units.WithValue != 2 || units.WithoutValue != 3 {
		t.Errorf("with=%d without=%d, want 2 and 3 summing to 5", units.WithValue, units.WithoutValue)
	}
}

// A key that no repository sets is its own state and is reported, never silently dropped: it is how
// an operator finds out they configured a key the organization does not administer.
func TestAKeyNobodySetsIsReportedRatherThanEmpty(t *testing.T) {
	s := ownedStore(t, "has-ghas/a")

	units, err := s.PropertyBreakdown("no_such_key")
	if err != nil {
		t.Fatal(err)
	}
	if len(units.Values) != 0 {
		t.Errorf("values %+v, want none", units.Values)
	}
	if units.WithoutValue != 1 || units.Repositories != 1 {
		t.Errorf("without=%d of %d, want the repository counted as having no value",
			units.WithoutValue, units.Repositories)
	}
}

// Property names are matched case-insensitively upstream, so two spellings of one key must not
// become two keys here.
func TestAPropertyNameIsOneKeyRegardlessOfSpelling(t *testing.T) {
	s := ownedStore(t, "has-ghas/a")
	if err := s.ReplacePropertyValues("has-ghas/a", []PropertyValue{
		{Name: "Business_ID", Value: "3"}}); err != nil {
		t.Fatal(err)
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(units.Values) != 1 || units.Values[0].Value != "3" {
		t.Errorf("breakdown %+v, want the value found under either spelling", units.Values)
	}
}
