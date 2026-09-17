package store

import "testing"

// The advisories unit answers "what reaches me, worst first". It is built on `abom_exposure`, which
// holds only advisories that reach a repository under review — which is why the coverage strip
// counted 11 live advisories while the table held 3. The unit states the difference rather than
// leaving a reader to notice a critical one missing and have no way to learn why.
func TestAdvisoriesPageSaysHowManyLiveAdvisoriesReachNothing(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	advisoryAgainst(t, s, "GHSA-reaching", "high", "github/codeql-action", "3.0", "")
	// Names an action nothing under review uses, so it reaches nobody's supply chain.
	advisoryAgainst(t, s, "GHSA-elsewhere", "critical", "some/other-action", "", "")
	exposeRepo(t, s, "octo-org/app", "GHSA-reaching", "github/codeql-action/init", 1)

	page, err := s.AdvisoriesPage(AdvisoriesQuery{})
	if err != nil {
		t.Fatalf("AdvisoriesPage: %v", err)
	}
	if page.Unreached != 1 {
		t.Errorf("unreached = %d, want the 1 advisory that reaches nothing under review",
			page.Unreached)
	}
	for _, it := range page.Items {
		if it.GHSAID == "GHSA-elsewhere" {
			t.Error("an advisory reaching nothing under review is listed as if it did")
		}
	}
}

func TestAdvisoriesPageReportsHowManyRowsItLeftOut(t *testing.T) {
	s := advisoryStore(t)
	for i, name := range []string{"a/one", "b/two", "c/three"} {
		seedDependency(t, s, "octo-org/app", name, "v1", i+1)
		advisoryAgainst(t, s, "GHSA-"+name[:1], "high", name, "9.9", "")
		exposeRepo(t, s, "octo-org/app", "GHSA-"+name[:1], name, 1)
	}

	page, err := s.AdvisoriesPage(AdvisoriesQuery{Limit: 1})
	if err != nil {
		t.Fatalf("AdvisoriesPage: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page holds %d rows, want the 1 asked for", len(page.Items))
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want the whole match rather than the page", page.Total)
	}
}

// Severity times reach, and nothing else. Ordering the page rather than the match would name the
// worst advisory on this page, which is not what the reader asked.
func TestAdvisoriesPageOrdersByPriorityAcrossTheWholeMatch(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "low/reach", "v1", 1)
	seedDependency(t, s, "octo-org/app", "high/reach", "v1", 2)
	advisoryAgainst(t, s, "GHSA-low", "low", "low/reach", "9.9", "")
	advisoryAgainst(t, s, "GHSA-critical", "critical", "high/reach", "9.9", "")
	exposeRepo(t, s, "octo-org/app", "GHSA-low", "low/reach", 1)
	exposeRepo(t, s, "octo-org/app", "GHSA-critical", "high/reach", 1)

	page, err := s.AdvisoriesPage(AdvisoriesQuery{Limit: 1})
	if err != nil {
		t.Fatalf("AdvisoriesPage: %v", err)
	}
	if len(page.Items) == 0 || page.Items[0].GHSAID != "GHSA-critical" {
		t.Errorf("first row = %+v, want the highest priority across the match", page.Items)
	}
}
