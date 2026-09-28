package main

import (
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A requirement for native locking is a policy like any other, asked through the rule that
// reports it: explicit, simulated before it binds, and refusing by rule ID, which is the only
// source of a verdict. ABOM's own reading of the lock is evidence beside it and never a term.
//
// The lock rule should fire exactly where ABOM's reading leaves a reference without a usable lock.
func TestALockRequirementIsAPolicyOverTheLockRuleAndAgreesWithTheReading(t *testing.T) {
	s := builtCorpus(t)
	if err := s.SavePolicy(store.Policy{Name: "require-locks",
		Predicate: "rules=abom/actions/uncovered-action-ref", CreatedBy: "test"}); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if err := s.AdoptPolicy("require-locks", "test"); err == nil {
		t.Fatal("a lock requirement was adopted without a simulation")
	}
	outcome, simulation, err := s.SimulatePolicy("require-locks", "")
	if err != nil {
		t.Fatalf("SimulatePolicy: %v", err)
	}
	if simulation.Refused == 0 {
		t.Fatal("the simulation refused nothing, so the agreement below would be vacuous")
	}
	if err := s.AdoptPolicy("require-locks", "test"); err != nil {
		t.Fatalf("AdoptPolicy after a simulation: %v", err)
	}

	for _, repository := range []string{"lockedcorp/partial-adoption", "globex-industries/dependency-lock"} {
		if !slices.Contains(outcome.Refused, repository) {
			t.Errorf("%s is not refused; its lock leaves a reference uncovered", repository)
		}
	}
	for _, repository := range []string{"lockedcorp/pinned-service", "lockedcorp/stale-selection"} {
		if slices.Contains(outcome.Refused, repository) {
			t.Errorf("%s is refused, though its lock covers every reference", repository)
		}
	}

	// Every refusal sits where the reading says a lockfile exists and leaves something uncovered.
	for _, repository := range outcome.Refused {
		var uncovered int
		if err := s.DB().QueryRow(`
			SELECT COUNT(*) FROM abom_lock_coverage
			WHERE repository = ? AND ((workflow_path = '' AND coverage = 'unsupported')
			                          OR (workflow_path <> '' AND coverage = 'not_onboarded'))`,
			repository).Scan(&uncovered); err != nil {
			t.Fatal(err)
		}
		if uncovered == 0 {
			t.Errorf("%s is refused, and the reading of its lock says nothing is uncovered", repository)
		}
	}
}
