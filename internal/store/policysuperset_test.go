package store

import (
	"slices"
	"strings"
	"testing"
)

// supersetFixture is the policy fixture plus a high advisory octo-org/extra reaches directly, so a
// policy about findings and one about advisories each refuse something the other does not.
func supersetFixture(t *testing.T, policies ...Policy) *Store {
	t.Helper()
	s := policyFixture(t, unpinned("unpinned"))
	advisoryAgainst(t, s, "GHSA-high", "high", "actions/checkout", "2.0", "")
	advisoryAgainst(t, s, "GHSA-crit-nowhere", "critical", "some/other-action", "", "")
	exposeRepo(t, s, "octo-org/extra", "GHSA-high", "actions/checkout", 1)
	for _, p := range policies {
		if err := s.SavePolicy(p); err != nil {
			t.Fatalf("SavePolicy(%s): %v", p.Name, err)
		}
	}
	return s
}

func policy(name, predicate string) Policy {
	return Policy{Name: name, Predicate: predicate, CreatedBy: "cli"}
}

// A policy about advisories refuses the owned repositories an advisory reaches, by the route asked,
// and says how many advisories at that severity reach nothing it could refuse.
func TestAnAdvisoryPolicyRefusesTheOwnedRepositoriesAnAdvisoryReaches(t *testing.T) {
	s := supersetFixture(t, policy("advisories", "advisorySeverity=critical,high"))

	out, err := s.EvaluatePolicy("advisories", "")
	if err != nil {
		t.Fatalf("EvaluatePolicy: %v", err)
	}
	if !slices.Equal(out.Refused, []string{"octo-org/extra"}) {
		t.Errorf("refused %v, want the repository the advisory reaches", out.Refused)
	}
	if out.Population != PopulationAdvisories || out.Advisories == nil || *out.Advisories != 1 ||
		out.Findings != 0 || out.Nodes != 1 {
		t.Errorf("outcome %+v, want one advisory on one node and no findings", out)
	}
	if out.Unreached == nil || *out.Unreached != 1 {
		t.Errorf("unreached = %v, want the critical advisory reaching nothing counted", out.Unreached)
	}

	transitive := policy("transitive", "advisorySeverity=high&reached=transitive")
	if err := s.SavePolicy(transitive); err != nil {
		t.Fatal(err)
	}
	if out, err := s.EvaluatePolicy("transitive", ""); err != nil || len(out.Refused) != 0 {
		t.Errorf("a transitive-only policy refused %v (%v), and the only route is direct",
			out.Refused, err)
	}
}

// A finding policy says nothing about advisories, and says so by leaving the counts out rather
// than reporting zero of something it never looked for.
func TestAFindingPolicyDoesNotReportAdvisoriesItNeverAskedAbout(t *testing.T) {
	s := supersetFixture(t)
	out, err := s.EvaluatePolicy("unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Population != PopulationFindings || out.Advisories != nil || out.Unreached != nil {
		t.Errorf("outcome %+v, want a findings policy with no advisory counts", out)
	}
}

// Written past SavePolicy, because a store can hold one from before the refusal existed, and the
// evaluation is what must not enforce either half of it.
func TestAPolicyAskingAboutFindingsAndAdvisoriesIsRefused(t *testing.T) {
	s := supersetFixture(t)
	if _, err := s.db.Exec(`UPDATE abom_policy SET predicate = ? WHERE name = ?`,
		"advisorySeverity=high&rules=actions/unpinned-tag", "unpinned"); err != nil {
		t.Fatal(err)
	}
	out, err := s.EvaluatePolicy("unpinned", "")
	if err == nil || !strings.Contains(err.Error(), "policies=") {
		t.Errorf("EvaluatePolicy = %v, want a refusal naming the way to ask both", err)
	}
	if len(out.Refused) != 0 {
		t.Errorf("refused %v, want nothing", out.Refused)
	}
}

// A superset refuses what any policy it names refuses: the OR a single predicate cannot say.
func TestASupersetRefusesWhatAnyPolicyItNamesRefuses(t *testing.T) {
	s := supersetFixture(t,
		policy("advisories", "advisorySeverity=high"),
		policy("either", "policies=unpinned,advisories"))

	out, err := s.EvaluatePolicy("either", "")
	if err != nil {
		t.Fatalf("EvaluatePolicy: %v", err)
	}
	want := []string{"octo-org/app", "octo-org/extra", "octo-org/tool"}
	if !slices.Equal(out.Matched, want) || !slices.Equal(out.Refused, want) {
		t.Errorf("matched %v, refused %v; want %v for both", out.Matched, out.Refused, want)
	}
	if len(out.Members) != 2 || out.Members[0].Policy != "advisories" ||
		out.Members[1].Policy != "unpinned" {
		t.Errorf("members %+v, want each named policy's own outcome, in the predicate's order",
			out.Members)
	}
	if out.Population != PopulationPolicies || out.Advisories == nil || *out.Advisories != 1 ||
		out.Findings != out.Members[1].Findings {
		t.Errorf("outcome %+v, want the members' evidence carried whole", out)
	}
}

// Two members matching one finding are one finding. Summed, a superset would report more than the
// store holds, which is the direction an unearned number must never go.
func TestASupersetCountsWhatTwoMembersBothMatchOnce(t *testing.T) {
	s := supersetFixture(t,
		policy("tool", "rules=actions/unpinned-tag&repositories=octo-org/tool"),
		policy("both", "policies=unpinned,tool"))

	out, err := s.EvaluatePolicy("both", "")
	if err != nil {
		t.Fatal(err)
	}
	if sum := out.Members[0].Findings + out.Members[1].Findings; out.Findings >= sum {
		t.Errorf("findings = %d from members of %d and %d, want the overlap counted once",
			out.Findings, out.Members[0].Findings, out.Members[1].Findings)
	}
	if out.Findings != out.Members[1].Findings {
		t.Errorf("findings = %d, want %d: tool's are a subset of unpinned's",
			out.Findings, out.Members[1].Findings)
	}
}

// A member's waiver is part of what that member refuses; the superset's own waiver excuses from
// the union. Neither reaches past the policy it was granted against.
func TestASupersetHonorsItsMembersWaiversAndItsOwn(t *testing.T) {
	s := supersetFixture(t,
		policy("advisories", "advisorySeverity=high"),
		policy("either", "policies=unpinned,advisories"))
	for _, w := range []Waiver{
		{Policy: "unpinned", Kind: WaiverExclusion, Repository: "octo-org/tool",
			Reason: "archived", GrantedBy: "alice"},
		{Policy: "either", Kind: WaiverExclusion, Repository: "octo-org/extra",
			Reason: "training target", GrantedBy: "bob"},
	} {
		if err := s.GrantWaiver(w); err != nil {
			t.Fatal(err)
		}
	}

	out, err := s.EvaluatePolicy("either", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Refused, []string{"octo-org/app"}) {
		t.Errorf("refused %v, want only the repository no waiver excuses", out.Refused)
	}
	by := map[string]string{}
	for _, w := range out.Waived {
		by[w.Repository] = w.Policy
	}
	if by["octo-org/tool"] != "unpinned" || by["octo-org/extra"] != "either" {
		t.Errorf("waived %+v, want each excused repository named with the waiver excusing it", out.Waived)
	}
}

// A superset naming a policy that does not exist, or one that names it back, has no answer. It is
// refused where it is written, and again where it is read.
func TestASupersetNamingAMissingPolicyOrItselfIsRefused(t *testing.T) {
	s := supersetFixture(t)
	if err := s.SavePolicy(policy("ghost", "policies=nobody")); err == nil ||
		!strings.Contains(err.Error(), "nobody") {
		t.Errorf("SavePolicy(policies=nobody) = %v, want the missing policy named", err)
	}
	if err := s.SavePolicy(policy("a", "policies=unpinned")); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePolicy(policy("unpinned", "policies=a")); err == nil ||
		!strings.Contains(err.Error(), "a") {
		t.Errorf("SavePolicy closing a cycle = %v, want it refused", err)
	}
	if err := s.SavePolicy(policy("self", "policies=self")); err == nil {
		t.Error("a superset naming itself was saved")
	}

	if _, err := s.db.Exec(`UPDATE abom_policy SET predicate = ? WHERE name = ?`,
		"policies=a", "unpinned"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluatePolicy("a", ""); err == nil {
		t.Error("a cycle written past SavePolicy was evaluated")
	}
}

// Editing a policy un-adopts it, because what was simulated is no longer what is written. A
// superset naming it changed meaning in the same edit, so it is un-adopted with it.
func TestEditingAPolicyUnadoptsTheSupersetsNamingIt(t *testing.T) {
	s := supersetFixture(t, policy("either", "policies=unpinned"),
		policy("outer", "policies=either"))
	for _, name := range []string{"either", "outer"} {
		if _, _, err := s.SimulatePolicy(name, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.AdoptPolicy(name, "alice"); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.SavePolicy(policy("unpinned", "rules=actions/*")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"either", "outer"} {
		p, _, err := s.PolicyNamed(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.State != PolicyDraft {
			t.Errorf("%s is %s after a policy it names was edited, want draft", name, p.State)
		}
	}
}

// What a simulation of an advisory policy found is recorded with it, and a finding policy records
// no advisory count rather than zero.
func TestASimulationRecordsTheAdvisoriesItFound(t *testing.T) {
	s := supersetFixture(t, policy("advisories", "advisorySeverity=high"))
	for name, want := range map[string]*int{"advisories": new(int), "unpinned": nil} {
		if _, _, err := s.SimulatePolicy(name, ""); err != nil {
			t.Fatal(err)
		}
		run, ok, err := s.LatestSimulation(name)
		if err != nil || !ok {
			t.Fatalf("LatestSimulation(%s): %v %v", name, ok, err)
		}
		switch {
		case want == nil && run.Advisories != nil:
			t.Errorf("%s recorded %d advisories, want none recorded", name, *run.Advisories)
		case want != nil && (run.Advisories == nil || *run.Advisories != 1):
			t.Errorf("%s recorded %v advisories, want 1", name, run.Advisories)
		}
	}
}
