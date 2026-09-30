package store

import (
	"strings"
	"testing"
)

// A sort name the whitelist accepts is one the interface may send, so each has to order a page in
// both directions. A name mapped to no expression once reached SQL as `ORDER BY  DESC`, which is
// a syntax error served as a broken grid rather than a refused request.
func TestEverySortAUnitAcceptsOrdersItsPage(t *testing.T) {
	s := newTestStore(t)

	for _, desc := range []bool{false, true} {
		for name := range inventorySorts {
			if _, err := s.ActionsInventoryPage(InventoryQuery{Sort: name, Desc: desc, Limit: 5}); err != nil {
				t.Errorf("inventory sort %q (desc=%v): %v", name, desc, err)
			}
		}
		for name := range repositorySorts {
			if _, err := s.RepositoriesPage(RepositoriesQuery{Sort: name, Desc: desc, Limit: 5}); err != nil {
				t.Errorf("repositories sort %q (desc=%v): %v", name, desc, err)
			}
		}
		for name := range workflowSorts {
			if _, err := s.WorkflowsPage(WorkflowsQuery{Sort: name, Desc: desc, Limit: 5}); err != nil {
				t.Errorf("workflows sort %q (desc=%v): %v", name, desc, err)
			}
		}
		for name := range jobSorts {
			if _, err := s.JobsPage(JobsQuery{Sort: name, Desc: desc, Limit: 5}); err != nil {
				t.Errorf("jobs sort %q (desc=%v): %v", name, desc, err)
			}
		}
	}
}

// `severity` was the name of this ordering before findings took it over, so an old link can still
// ask for it. It is refused by name, with what is on offer, not attempted.
func TestARetiredSortIsRefusedByName(t *testing.T) {
	s := newTestStore(t)

	_, err := s.ActionsInventoryPage(InventoryQuery{Sort: "severity", Limit: 5})
	if err == nil {
		t.Fatal("sort=severity was accepted, though no ordering is registered for it")
	}
	if !strings.Contains(err.Error(), `"severity"`) || !strings.Contains(err.Error(), "findings") {
		t.Errorf("refusal = %q, want it to name the sort and what is on offer", err)
	}
}
