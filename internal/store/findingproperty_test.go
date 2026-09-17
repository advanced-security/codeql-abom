package store

import (
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// propertyFixture puts the two repositories carrying findings into different business units and
// the same application, so that an AND across keys and an OR within one look different from each
// other and from a filter that does nothing.
func propertyFixture(t *testing.T) *Store {
	t.Helper()
	s := matchFixture(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for repo, unit := range map[string]string{"octo-org/app": "3", "octo-org/tool": "1"} {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: unit},
			{Name: "app_name", Value: "billing"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func match(name, value string) viewspec.PropertyMatch {
	return viewspec.PropertyMatch{Name: name, Value: value}
}

func repositoriesIn(t *testing.T, s *Store, sel FindingSelector) map[string]bool {
	t.Helper()
	got, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, n := range got.Nodes {
		for _, r := range n.Repositories {
			out[r] = true
		}
	}
	return out
}

// The whole point of the term. Both repositories carry the rule; only one is in unit 3.
func TestAPropertyFilterKeepsOnlyTheRepositoriesCarryingThatValue(t *testing.T) {
	s := propertyFixture(t)

	all := repositoriesIn(t, s, FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if len(all) != 2 {
		t.Fatalf("unfiltered saw %v, want both repositories", all)
	}

	only := repositoriesIn(t, s, FindingSelector{
		Rules:      []string{"actions/unpinned-tag"},
		Properties: []viewspec.PropertyMatch{match("business_id", "3")},
	})
	if len(only) != 1 || !only["octo-org/app"] {
		t.Errorf("business_id 3 matched %v, want only octo-org/app", only)
	}
}

// Several values of one key are an OR, because a multi-select property matches on any of its
// values and the store holds one row per value.
func TestSeveralValuesOfOneKeyAreAnOr(t *testing.T) {
	s := propertyFixture(t)

	got := repositoriesIn(t, s, FindingSelector{
		Rules:      []string{"actions/unpinned-tag"},
		Properties: []viewspec.PropertyMatch{match("business_id", "3"), match("business_id", "1")},
	})
	if len(got) != 2 {
		t.Errorf("business_id 3 or 1 matched %v, want both repositories", got)
	}
}

// Several keys are an AND. Without this a scope naming a unit and an application would answer the
// union — every repository in either — while reading as though it named their intersection.
func TestSeveralKeysAreAnAnd(t *testing.T) {
	s := propertyFixture(t)

	both := repositoriesIn(t, s, FindingSelector{
		Rules:      []string{"actions/unpinned-tag"},
		Properties: []viewspec.PropertyMatch{match("business_id", "3"), match("app_name", "billing")},
	})
	if len(both) != 1 || !both["octo-org/app"] {
		t.Errorf("unit 3 and app billing matched %v, want only octo-org/app", both)
	}

	neither := repositoriesIn(t, s, FindingSelector{
		Rules:      []string{"actions/unpinned-tag"},
		Properties: []viewspec.PropertyMatch{match("business_id", "3"), match("app_name", "payroll")},
	})
	if len(neither) != 0 {
		t.Errorf("unit 3 and app payroll matched %v, want nothing", neither)
	}
}

// Exclusion drops a repository the organization says is somebody else's, so a scope can be written
// as everything-but rather than by listing what is left.
func TestAnExcludedValueIsDropped(t *testing.T) {
	s := propertyFixture(t)

	got := repositoriesIn(t, s, FindingSelector{
		Rules:             []string{"actions/unpinned-tag"},
		ExcludeProperties: []viewspec.PropertyMatch{match("business_id", "1")},
	})
	if len(got) != 1 || !got["octo-org/app"] {
		t.Errorf("excluding business_id 1 left %v, want only octo-org/app", got)
	}
}

// Exclusion beats inclusion, so a scope cannot be both asked for and refused and quietly resolve
// to asked for.
func TestAnExcludedValueIsDroppedEvenWhenAlsoIncluded(t *testing.T) {
	s := propertyFixture(t)

	got := repositoriesIn(t, s, FindingSelector{
		Rules:             []string{"actions/unpinned-tag"},
		Properties:        []viewspec.PropertyMatch{match("business_id", "3"), match("business_id", "1")},
		ExcludeProperties: []viewspec.PropertyMatch{match("business_id", "1")},
	})
	if len(got) != 1 || !got["octo-org/app"] {
		t.Errorf("including both and excluding 1 left %v, want only octo-org/app", got)
	}
}

// A key nobody administers matches nothing rather than everything. The refusal this replaces
// existed because the key came from configuration and could be absent; now it is written in the
// question, so the honest failure is an empty answer rather than an error.
func TestAKeyTheOrganizationDoesNotAdministerMatchesNothing(t *testing.T) {
	s := propertyFixture(t)

	got := repositoriesIn(t, s, FindingSelector{
		Rules:      []string{"actions/unpinned-tag"},
		Properties: []viewspec.PropertyMatch{match("cost_center", "3")},
	})
	if len(got) != 0 {
		t.Errorf("an unadministered key matched %v, want nothing", got)
	}
}
