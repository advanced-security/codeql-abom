package store

import (
	"slices"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// selectorOf parses a predicate the way a policy and the match endpoint both do.
func selectorOf(t *testing.T, text string) FindingSelector {
	t.Helper()
	spec, err := viewspec.Parse(text)
	if err != nil {
		t.Fatalf("Parse(%s): %v", text, err)
	}
	return SelectorFor(spec)
}

// chargedTo is each matched node's repositories, which is what a policy refuses.
func chargedTo(t *testing.T, s *Store, text string) (map[string][]string, FindingMatch) {
	t.Helper()
	got, err := s.NodesMatching(selectorOf(t, text))
	if err != nil {
		t.Fatalf("NodesMatching(%s): %v", text, err)
	}
	out := map[string][]string{}
	for _, n := range got.Nodes {
		out[n.Key] = n.Repositories
	}
	return out, got
}

// reachFixture holds three findings found in octo-org/app: one on an action other and tool reach,
// one inside a composite action tool reaches, and one in app's own workflow, which nothing reaches
// because nobody depends on it.
func reachFixture(t *testing.T) *Store {
	t.Helper()
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "vendor/action", "v1", 1)
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "vendor/composite", Kind: "composite_action", IsExternal: true, Owner: "vendor",
			Repo: "composite"},
		{Key: "vendor/composite-step", Kind: "step", ContainerKey: "vendor/composite"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	scoredOn(t, s, "vendor/action", "actions/code-injection/critical", "error", score(9.0))
	scoredOn(t, s, "vendor/composite-step", "actions/untrusted-checkout/high", "error", score(8.0))
	scoredOn(t, s, "octo-org/app/.github/workflows/ci.yml", "actions/unpinned-tag", "warning",
		score(5.0))
	for _, r := range []struct {
		node, repo string
		hops       int
	}{
		{"vendor/action", "octo-org/other", 1},
		{"vendor/action", "octo-org/tool", 2},
		{"vendor/composite", "octo-org/tool", 1},
	} {
		if _, err := s.UpsertRepository(r.repo, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops)
			SELECT n.row_id, r.row_id, ? FROM abom_node n, repository r
			 WHERE n.node_key = ? AND r.repository_full_name = ?`, r.hops, r.node, r.repo); err != nil {
			t.Fatalf("recording reach: %v", err)
		}
	}
	return s
}

// Without the term nothing moves: a finding is charged to the repository it was found in, which is
// what every policy on record already refuses.
func TestWithoutReachedAFindingIsChargedToWhereItWasFound(t *testing.T) {
	s := reachFixture(t)
	got, _ := chargedTo(t, s, "rules=actions/*")
	if len(got) != 3 || !slices.Equal(got["vendor/action"], []string{"octo-org/app"}) {
		t.Errorf("charged %v, want all three to octo-org/app", got)
	}
}

// Under `reached` a finding is charged to the repositories that reach the node it sits on, or the
// composite action holding it, at the hops asked. A finding in a repository's own workflow is
// reached by nobody, because nothing depends on that workflow.
func TestReachedChargesAFindingToTheRepositoriesThatReachIt(t *testing.T) {
	s := reachFixture(t)
	for _, c := range []struct {
		text     string
		want     map[string][]string
		findings int
	}{
		{"rules=actions/*&reached=direct", map[string][]string{
			"vendor/action":         {"octo-org/other"},
			"vendor/composite-step": {"octo-org/tool"},
		}, 2},
		{"rules=actions/*&reached=transitive", map[string][]string{
			"vendor/action": {"octo-org/tool"},
		}, 1},
		{"rules=actions/*&reached=direct,transitive", map[string][]string{
			"vendor/action":         {"octo-org/other", "octo-org/tool"},
			"vendor/composite-step": {"octo-org/tool"},
		}, 2},
	} {
		got, match := chargedTo(t, s, c.text)
		if len(got) != len(c.want) {
			t.Errorf("%s: charged %v, want %v", c.text, got, c.want)
			continue
		}
		for key, repos := range c.want {
			if !slices.Equal(got[key], repos) {
				t.Errorf("%s: %s charged to %v, want %v", c.text, key, got[key], repos)
			}
		}
		if match.Findings != c.findings {
			t.Errorf("%s: %d findings, want %d counted once each", c.text, match.Findings, c.findings)
		}
	}
}

// The gate asks about the pull request's repository, which under `reached` is the one reaching the
// dependency — never the upstream repository the finding was found in.
func TestReachedNarrowsTheReachingRepository(t *testing.T) {
	s := reachFixture(t)
	got, _ := chargedTo(t, s, "rules=actions/*&reached=direct,transitive&repositories=octo-org/other")
	if len(got) != 1 || !slices.Equal(got["vendor/action"], []string{"octo-org/other"}) {
		t.Errorf("charged %v, want only the action octo-org/other reaches", got)
	}
	none, _ := chargedTo(t, s, "rules=actions/*&reached=direct&repositories=octo-org/app")
	if len(none) != 0 {
		t.Errorf("charged %v to the repository the findings were found in, which reaches none", none)
	}
}

// advisoryMatchFixture holds a critical advisory octo-org/a reaches directly and octo-org/b through
// an intermediary, a moderate one and an unrated one octo-org/c reaches, and a withdrawn critical.
func advisoryMatchFixture(t *testing.T) *Store {
	t.Helper()
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-crit", "critical", "actions/checkout", "2.0", "")
	advisoryAgainst(t, s, "GHSA-mod", "moderate", "actions/cache", "2.0", "")
	advisoryAgainst(t, s, "GHSA-none", "", "actions/checkout", "", "")
	advisoryAgainst(t, s, "GHSA-gone", "critical", "actions/cache", "2.0", "2026-01-01T00:00:00Z")
	exposeRepo(t, s, "octo-org/a", "GHSA-crit", "actions/checkout", 1)
	exposeRepo(t, s, "octo-org/b", "GHSA-crit", "actions/checkout", 2)
	exposeRepo(t, s, "octo-org/c", "GHSA-mod", "actions/cache", 1)
	exposeRepo(t, s, "octo-org/c", "GHSA-none", "actions/checkout", 1)
	exposeRepo(t, s, "octo-org/a", "GHSA-gone", "actions/cache", 1)
	return s
}

// An advisory term asks about what a live advisory names and who reaches it, at the severity GitHub
// published. It says nothing about whether a reference is inside the range: that is a rule's call.
func TestAnAdvisoryTermMatchesWhatALiveAdvisoryNamesAndWhoReachesIt(t *testing.T) {
	s := advisoryMatchFixture(t)
	for _, c := range []struct {
		text string
		want map[string][]string
	}{
		{"advisorySeverity=critical", map[string][]string{
			"actions/checkout": {"octo-org/a", "octo-org/b"}}},
		{"advisorySeverity=medium", map[string][]string{"actions/cache": {"octo-org/c"}}},
		{"advisorySeverity=unrated", map[string][]string{"actions/checkout": {"octo-org/c"}}},
		{"advisorySeverity=critical&reached=direct", map[string][]string{
			"actions/checkout": {"octo-org/a"}}},
		{"advisorySeverity=critical&reached=transitive", map[string][]string{
			"actions/checkout": {"octo-org/b"}}},
		{"advisorySeverity=critical&repositories=octo-org/b", map[string][]string{
			"actions/checkout": {"octo-org/b"}}},
	} {
		got, match := chargedTo(t, s, c.text)
		if len(got) != len(c.want) {
			t.Errorf("%s: charged %v, want %v", c.text, got, c.want)
			continue
		}
		for key, repos := range c.want {
			if !slices.Equal(got[key], repos) {
				t.Errorf("%s: %s charged to %v, want %v", c.text, key, got[key], repos)
			}
		}
		if match.Population != PopulationAdvisories || match.Advisories != 1 || match.Findings != 0 {
			t.Errorf("%s: population %q, %d advisories, %d findings; want one advisory and no findings",
				c.text, match.Population, match.Advisories, match.Findings)
		}
	}

	_, all := chargedTo(t, s, "advisorySeverity=critical,high,medium,low,unrated")
	if all.Advisories != 3 {
		t.Errorf("every severity matched %d advisories, want the 3 live ones", all.Advisories)
	}
	for _, n := range all.Nodes {
		if slices.Contains(n.Advisories, "GHSA-gone") {
			t.Error("a withdrawn advisory is matched")
		}
	}
}

// The advisories unit answers the same two terms a policy does, so a reader can see what a policy
// about advisories would refuse before writing it. What it leaves out is counted at the severity
// asked, or the line under the table accounts for advisories the table never claimed to hold.
func TestTheAdvisoriesUnitNarrowsByPublishedSeverityAndRoute(t *testing.T) {
	s := advisoryMatchFixture(t)
	advisoryAgainst(t, s, "GHSA-crit-nowhere", "critical", "some/other-action", "", "")

	page := func(q AdvisoriesQuery) (map[string]RemediationItem, int) {
		t.Helper()
		got, err := s.AdvisoriesPage(q)
		if err != nil {
			t.Fatalf("AdvisoriesPage(%+v): %v", q, err)
		}
		out := map[string]RemediationItem{}
		for _, it := range got.Items {
			out[it.GHSAID] = it
		}
		return out, got.Unreached
	}

	critical, unreached := page(AdvisoriesQuery{Severities: []string{"critical"}})
	if len(critical) != 1 || critical["GHSA-crit"].DirectRepos != 1 ||
		critical["GHSA-crit"].TransitiveRepos != 1 {
		t.Errorf("critical lists %+v, want GHSA-crit reaching one repository each way", critical)
	}
	if unreached != 1 {
		t.Errorf("unreached = %d under critical, want the one critical advisory reaching nothing", unreached)
	}
	medium, unreached := page(AdvisoriesQuery{Severities: []string{"medium"}})
	if _, ok := medium["GHSA-mod"]; len(medium) != 1 || !ok || unreached != 0 {
		t.Errorf("medium lists %+v with %d unreached, want the moderate advisory alone", medium, unreached)
	}
	direct, _ := page(AdvisoriesQuery{Severities: []string{"critical"},
		Reached: []string{"direct"}})
	if it := direct["GHSA-crit"]; len(direct) != 1 || it.DirectRepos != 1 || it.TransitiveRepos != 0 {
		t.Errorf("critical and direct lists %+v, want only the direct route counted", direct)
	}
	// The moderate advisory is reached, directly; a transitive-only question leaves it out, and it
	// is not one that "nothing you own uses".
	none, unreached := page(AdvisoriesQuery{Severities: []string{"medium"},
		Reached: []string{"transitive"}})
	if len(none) != 0 || unreached != 0 {
		t.Errorf("medium and transitive lists %+v with %d unreached, want neither", none, unreached)
	}
}
