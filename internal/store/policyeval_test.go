package store

import (
	"testing"
	"time"
)

// policyFixture is the match fixture with the account declared, because a policy is evaluated over
// what the operator owns. `octo-org/app` carries `actions/unpinned-tag` and `abom/secrets-inherit`;
// `octo-org/tool` carries `actions/unpinned-tag`.
func policyFixture(t *testing.T, p Policy) *Store {
	t.Helper()
	s := matchFixture(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePolicy(p); err != nil {
		t.Fatal(err)
	}
	return s
}

func unpinned(name string) Policy {
	return Policy{
		Name: name, Predicate: "rules=actions/unpinned-tag",
		ScopeKind: PolicyScopeOrganization, CreatedBy: "cli",
	}
}

// A waiver excuses a repository; it does not pretend the finding is not there. Hiding the match
// would make an excused repository indistinguishable from a clean one, and the whole point of the
// audit answer is that somebody can see what is being let through.
func TestAWaivedRepositoryStillMatchesAndIsNotRefused(t *testing.T) {
	s := policyFixture(t, unpinned("no-unpinned"))

	before, err := s.EvaluatePolicy("no-unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Refused) != 2 {
		t.Fatalf("refused %v, want both repositories carrying the rule", before.Refused)
	}

	if err := s.GrantWaiver(Waiver{
		Policy: "no-unpinned", Kind: WaiverExclusion, Repository: "octo-org/app",
		Reason: "training target", GrantedBy: "alice"}); err != nil {
		t.Fatal(err)
	}

	after, err := s.EvaluatePolicy("no-unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Refused) != 1 || after.Refused[0] != "octo-org/tool" {
		t.Errorf("refused %v, want only the repository with no waiver", after.Refused)
	}
	if len(after.Matched) != 2 {
		t.Errorf("matched %v, want the waived repository still counted as matching", after.Matched)
	}
	if len(after.Waived) != 1 || after.Waived[0].Repository != "octo-org/app" {
		t.Errorf("waived %+v, want the excused repository named with its waiver", after.Waived)
	}
}

// An expired exception stops applying without anybody touching it, and the thing it was waiving is
// refused on the next evaluation.
func TestAnExpiredExceptionIsRefusedOnTheNextEvaluation(t *testing.T) {
	s := policyFixture(t, unpinned("no-unpinned"))
	past := time.Now().Add(-time.Minute)
	if err := s.grantWaiverAt(Waiver{
		Policy: "no-unpinned", Kind: WaiverException, Repository: "octo-org/app",
		Reason: "migration", GrantedBy: "alice", ExpiresAt: past}, past.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	out, err := s.EvaluatePolicy("no-unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Refused) != 2 {
		t.Errorf("refused %v, want the expired exception to stop excusing anything", out.Refused)
	}
	if len(out.Waived) != 0 {
		t.Errorf("waived %+v, want nothing in force", out.Waived)
	}
}

// A policy adopted for one unit must not refuse another's repositories, which is the whole reason
// the middle scope exists: remediation has to be addressable to somebody.
func TestAUnitScopedPolicyRefusesOnlyItsOwnUnit(t *testing.T) {
	s := policyFixture(t, Policy{
		Name: "unit-three", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: PolicyScopeUnit, ScopeValue: "3", CreatedBy: "cli"})
	for repo, unit := range map[string]string{"octo-org/app": "3", "octo-org/tool": "1"} {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := s.EvaluatePolicy("unit-three", "business_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Refused) != 1 || out.Refused[0] != "octo-org/app" {
		t.Errorf("refused %v, want only unit 3's repository carrying the rule", out.Refused)
	}
}

// A unit scope with no key configured cannot narrow anything. Falling back to the whole
// organization would enforce a rule somewhere nobody scoped it to.
func TestAUnitScopedPolicyWithNoConfiguredKeyIsRefused(t *testing.T) {
	s := policyFixture(t, Policy{
		Name: "unit-three", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: PolicyScopeUnit, ScopeValue: "3", CreatedBy: "cli"})

	if _, err := s.EvaluatePolicy("unit-three", ""); err == nil {
		t.Fatal("a unit-scoped policy was evaluated against the whole organization")
	}
}

// A predicate that failed to parse must refuse nothing rather than everything, which is the loudest
// possible way to be wrong about the safest possible default.
func TestAPolicyThatCannotBeParsedRefusesNothing(t *testing.T) {
	s := policyFixture(t, unpinned("broken"))
	// Written past SavePolicy, because a corpus can still hold one from an older build, and the
	// evaluation is what must not turn that into a blocked estate.
	if _, err := s.db.Exec(
		`UPDATE abom_policy SET predicate = ? WHERE name = ?`, "severity=%zz", "broken"); err != nil {
		t.Fatal(err)
	}

	out, err := s.EvaluatePolicy("broken", "")
	if err == nil {
		t.Fatal("an unparseable policy was evaluated as though it said something")
	}
	if len(out.Refused) != 0 {
		t.Errorf("refused %v, want nothing", out.Refused)
	}
}

// A policy scoped to one repository refuses that one and says nothing about any other.
func TestARepositoryScopedPolicyRefusesOnlyThatRepository(t *testing.T) {
	s := policyFixture(t, Policy{
		Name: "just-tool", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: PolicyScopeRepository, ScopeValue: "octo-org/tool", CreatedBy: "cli"})

	out, err := s.EvaluatePolicy("just-tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Refused) != 1 || out.Refused[0] != "octo-org/tool" {
		t.Errorf("refused %v, want only the repository the policy is bound to", out.Refused)
	}
}
