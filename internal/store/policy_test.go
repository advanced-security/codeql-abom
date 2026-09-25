package store

import (
	"strings"
	"testing"
	"time"
)

func draft(t *testing.T, s *Store, name string) Policy {
	t.Helper()
	p := Policy{
		Name:      name,
		Predicate: "flags=findings&scope=owned",
		CreatedBy: "cli",
	}
	if err := s.SavePolicy(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A rule nobody can try is a rule nobody turns on, so a policy starts as something to ask questions
// about. Arriving already in force would be the gate switching itself on.
func TestAPolicyStartsAsADraft(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "no-unpinned-actions")

	got, ok, err := s.PolicyNamed("no-unpinned-actions")
	if err != nil || !ok {
		t.Fatalf("PolicyNamed: %v (found=%v)", err, ok)
	}
	if got.State != PolicyDraft {
		t.Errorf("state %q, want %q", got.State, PolicyDraft)
	}
	if !got.AdoptedAt.IsZero() {
		t.Error("a draft has never been adopted, so there is no adoption time to report")
	}
}

// Adoption without a simulation is how a gate gets enabled on a backlog, blocks everything, and is
// switched off within a week. The refusal has to name what is missing, or it reads as a bug.
func TestAPolicyNobodySimulatedCannotBeAdopted(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "no-unpinned-actions")

	err := s.AdoptPolicy("no-unpinned-actions", "alice")
	if err == nil {
		t.Fatal("a policy was adopted with nothing known about what it would block")
	}
	if !strings.Contains(err.Error(), "simulat") {
		t.Errorf("refusal %q does not say what is missing", err)
	}
}

// A policy that cannot say what it was expected to block cannot be argued about afterwards, so
// adoption records which simulation it was adopted against.
func TestAdoptionRecordsTheSimulationItWasAdoptedAgainst(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "no-unpinned-actions")
	run, err := s.RecordSimulation(Simulation{
		Policy: "no-unpinned-actions", Nodes: 12, Findings: 7, Repositories: 3, Refused: 3})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AdoptPolicy("no-unpinned-actions", "alice"); err != nil {
		t.Fatal(err)
	}

	got, _, err := s.PolicyNamed("no-unpinned-actions")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != PolicyAdopted || got.AdoptedBy != "alice" {
		t.Errorf("got state=%q by=%q, want adopted by alice", got.State, got.AdoptedBy)
	}
	if got.AdoptedSimulation != run {
		t.Errorf("adopted against simulation %d, want %d", got.AdoptedSimulation, run)
	}
}

// An exclusion says the rule does not apply here. It is enumerated, so it can be listed in full.
func TestAnExclusionNamesOneRepositoryAndDoesNotExpire(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")

	if err := s.GrantWaiver(Waiver{
		Policy: "p", Kind: WaiverExclusion, Repository: "has-ghas/juice-shop",
		Reason: "training target, deliberately vulnerable", GrantedBy: "alice",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Waivers("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Repository != "has-ghas/juice-shop" {
		t.Fatalf("waivers %+v, want the one repository named", got)
	}
	if !got[0].ExpiresAt.IsZero() {
		t.Error("an exclusion does not expire; it says the rule does not apply here")
	}
	if !got[0].InForce {
		t.Error("an exclusion with no expiry is always in force")
	}
}

// An exception says the rule applies and is waived until a stated time. One without an expiry is an
// exclusion nobody admitted to writing.
func TestAnExceptionWithoutAnExpiryIsRefused(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")

	err := s.GrantWaiver(Waiver{
		Policy: "p", Kind: WaiverException, Repository: "has-ghas/one",
		Reason: "migration in flight", GrantedBy: "alice",
	})
	if err == nil {
		t.Fatal("an exception was granted with no expiry, which is an exclusion in disguise")
	}
	if !strings.Contains(err.Error(), "expir") {
		t.Errorf("refusal %q does not say what is missing", err)
	}
}

// An expired exception stops applying without anybody touching it. Deciding that at write time, or
// on a sweep, leaves a waiver in force for as long as nobody runs the sweep.
func TestAnExpiredExceptionStopsApplyingOnItsOwn(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")
	past := time.Now().Add(-time.Hour)
	if err := s.grantWaiverAt(Waiver{
		Policy: "p", Kind: WaiverException, Repository: "has-ghas/one",
		Reason: "migration in flight", GrantedBy: "alice", ExpiresAt: past,
	}, past.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := s.Waivers("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("waivers %+v, want the expired one still listed", got)
	}
	// Listed, because the audit answer is every waiver ever granted; not in force, because its
	// time ran out. Deleting it would erase the fact that somebody once granted it.
	if got[0].InForce {
		t.Error("an exception past its expiry is still being applied")
	}
}

// A pattern cannot be audited, and it silently covers a repository created after it was written — a
// waiver nobody granted.
func TestAWaiverWrittenAsAPatternIsRefused(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")

	for _, pattern := range []string{"has-ghas/*", "*", "has-ghas/juice-*", "has-ghas/?ne", "*/one"} {
		err := s.GrantWaiver(Waiver{
			Policy: "p", Kind: WaiverExclusion, Repository: pattern,
			Reason: "convenience", GrantedBy: "alice",
		})
		if err == nil {
			t.Errorf("%q was accepted as a waiver; a pattern covers repositories nobody named", pattern)
			continue
		}
		if !strings.Contains(err.Error(), "pattern") {
			t.Errorf("refusal for %q was %q, which does not say why", pattern, err)
		}
	}
}

// A waiver with no reason is a waiver nobody can review. The audit answer exists to be read by
// somebody deciding whether it should still stand.
func TestAWaiverWithoutAReasonIsRefused(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")

	err := s.GrantWaiver(Waiver{
		Policy: "p", Kind: WaiverExclusion, Repository: "has-ghas/one", GrantedBy: "alice",
	})
	if err == nil {
		t.Fatal("a waiver was granted with no reason")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("refusal %q does not say what is missing", err)
	}
}

// Every exclusion and exception in force, listed in full, on demand. This is the thing that makes
// the design defensible, so it spans policies rather than needing one call each.
func TestTheAuditAnswerListsEveryWaiverAcrossEveryPolicy(t *testing.T) {
	s := newTestStore(t)
	draft(t, s, "p")
	draft(t, s, "q")
	future := time.Now().Add(24 * time.Hour)
	for _, w := range []Waiver{
		{Policy: "p", Kind: WaiverExclusion, Repository: "has-ghas/a", Reason: "training", GrantedBy: "alice"},
		{Policy: "q", Kind: WaiverException, Repository: "has-ghas/b", Reason: "migration", GrantedBy: "bob", ExpiresAt: future},
	} {
		if err := s.GrantWaiver(w); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.AllWaivers()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("audit listed %d waivers, want both", len(got))
	}
	for _, w := range got {
		if w.Reason == "" || w.GrantedBy == "" || w.GrantedAt.IsZero() {
			t.Errorf("waiver %+v cannot be reviewed by anybody", w)
		}
		if w.Kind == WaiverException && w.ExpiresAt.IsZero() {
			t.Errorf("exception %+v has no expiry in the audit answer", w)
		}
	}
}
