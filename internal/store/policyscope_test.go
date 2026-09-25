package store

import (
	"strings"
	"testing"
)

// legacyPolicy writes a policy the way a build before the retirement did, with its scope beside
// the predicate rather than in it. SavePolicy no longer can, which is the point.
func legacyPolicy(t *testing.T, s *Store, name, predicate, kind, value string) {
	t.Helper()
	if _, err := s.db.Exec(`
INSERT INTO abom_policy (name, predicate, scope_kind, scope_value, state, created_by, created_at)
VALUES (?, ?, ?, ?, ?, 'cli', '2026-09-01T00:00:00Z')`,
		name, predicate, kind, value, PolicyAdopted); err != nil {
		t.Fatal(err)
	}
}

func placeRepositories(t *testing.T, s *Store, units map[string]string) {
	t.Helper()
	for repo, unit := range units {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{{Name: "business_id", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}
}

// A second narrowing beside the predicate, evaluated by different code, is how two policy engines
// start. A unit-scoped policy migrates into the one predicate, and selects exactly the repositories
// it selected before: `octo-org/app` is unit 3 and carries the rule, `octo-org/tool` is unit 1.
func TestAUnitScopedPolicyMigratesIntoItsPredicateAndSelectsTheSameRepositories(t *testing.T) {
	s := policyFixture(t, unpinned("org-wide"))
	placeRepositories(t, s, map[string]string{"octo-org/app": "3", "octo-org/tool": "1"})
	legacyPolicy(t, s, "unit-three", "rules=actions/unpinned-tag", "unit", "3")

	report, err := s.MigratePolicyScopes("business_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Migrated) != 1 || report.Migrated[0] != "unit-three" || len(report.Left) != 0 {
		t.Fatalf("report = %+v, want unit-three migrated and nothing left", report)
	}

	p, _, err := s.PolicyNamed("unit-three")
	if err != nil {
		t.Fatal(err)
	}
	if p.Predicate != "properties=business_id%3A3&rules=actions%2Funpinned-tag" {
		t.Errorf("predicate = %q, want the unit written into it", p.Predicate)
	}
	// The same question, reworded: adoption is kept rather than being reset by the migration.
	if p.State != PolicyAdopted {
		t.Errorf("state = %q, want the adoption kept", p.State)
	}

	out, err := s.EvaluatePolicy("unit-three", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matched) != 1 || out.Matched[0] != "octo-org/app" {
		t.Errorf("matched %v, want only unit 3's repository", out.Matched)
	}
	// And the evidence is counted within the bound, which the old scope never did: it narrowed
	// the repository list after counting the organization's nodes and findings.
	whole, err := s.EvaluatePolicy("org-wide", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Findings >= whole.Findings {
		t.Errorf("the unit's policy counts %d findings and the organization's %d; want fewer",
			out.Findings, whole.Findings)
	}
}

func TestARepositoryScopedPolicyMigratesIntoItsPredicate(t *testing.T) {
	s := policyFixture(t, unpinned("org-wide"))
	legacyPolicy(t, s, "just-tool", "rules=actions/unpinned-tag", "repository", "octo-org/tool")

	if _, err := s.MigratePolicyScopes(""); err != nil {
		t.Fatal(err)
	}
	out, err := s.EvaluatePolicy("just-tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matched) != 1 || out.Matched[0] != "octo-org/tool" {
		t.Errorf("matched %v, want only the repository it was bound to", out.Matched)
	}
}

// A scope the predicate already narrows is an intersection, and an intersection that is empty
// cannot be written: the language has no empty list, and silence would widen it to everything.
func TestAScopeThePredicateContradictsIsLeftAndRefusedByName(t *testing.T) {
	s := policyFixture(t, unpinned("org-wide"))
	legacyPolicy(t, s, "both", "repositories=octo-org/app,octo-org/tool&rules=actions/unpinned-tag",
		"repository", "octo-org/tool")
	legacyPolicy(t, s, "neither", "repositories=octo-org/app&rules=actions/unpinned-tag",
		"repository", "octo-org/tool")

	report, err := s.MigratePolicyScopes("")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Migrated) != 1 || report.Migrated[0] != "both" {
		t.Errorf("migrated %v, want only the policy whose scope and predicate overlap", report.Migrated)
	}
	if reason, left := report.Left["neither"]; !left || !strings.Contains(reason, "octo-org/tool") {
		t.Errorf("left %+v, want neither named with the repository it cannot reach", report.Left)
	}

	p, _, err := s.PolicyNamed("both")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Predicate, "repositories=octo-org%2Ftool&") {
		t.Errorf("predicate = %q, want the intersection", p.Predicate)
	}

	// Refused rather than evaluated either way: the scope it cannot express is still its text.
	if _, err := s.EvaluatePolicy("neither", ""); err == nil ||
		!strings.Contains(err.Error(), "retired") {
		t.Errorf("an unmigrated scope was evaluated: %v", err)
	}
}

// A unit scope needs the property carrying the dividing line, which is configuration the store does
// not hold. With none it stays as it was and the evaluation refuses it by name, which is what it
// did before; answering for the whole organization would enforce a rule nobody scoped there.
func TestAUnitScopeWithNoPropertyConfiguredIsLeftAndRefused(t *testing.T) {
	s := policyFixture(t, unpinned("org-wide"))
	legacyPolicy(t, s, "unit-three", "rules=actions/unpinned-tag", "unit", "3")

	report, err := s.MigratePolicyScopes("")
	if err != nil {
		t.Fatal(err)
	}
	if _, left := report.Left["unit-three"]; !left {
		t.Fatalf("report = %+v, want unit-three left", report)
	}
	_, err = s.EvaluatePolicy("unit-three", "")
	if err == nil || !strings.Contains(err.Error(), "--group-property") {
		t.Errorf("err = %v, want a refusal naming what would migrate it", err)
	}
}

// Migrating twice changes nothing the second time.
func TestMigratingPolicyScopesIsIdempotent(t *testing.T) {
	s := policyFixture(t, unpinned("org-wide"))
	legacyPolicy(t, s, "just-tool", "rules=actions/unpinned-tag", "repository", "octo-org/tool")

	if _, err := s.MigratePolicyScopes(""); err != nil {
		t.Fatal(err)
	}
	again, err := s.MigratePolicyScopes("")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Migrated) != 0 || len(again.Left) != 0 {
		t.Errorf("a second migration reported %+v", again)
	}
}
