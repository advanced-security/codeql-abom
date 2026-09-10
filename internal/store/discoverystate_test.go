package store

import "testing"

// A database nobody has started is not one somebody paused. Starting a crawl on an installation
// that never asked for one spends somebody's rate limit on a decision they did not make.
func TestAnInstallationNobodyStartedHasNoIntent(t *testing.T) {
	s := newTestStore(t)

	running, known, err := s.DiscoveryIntent()
	if err != nil {
		t.Fatal(err)
	}
	if known {
		t.Error("nobody has asked for anything, so there is no intent to read")
	}
	if running {
		t.Error("an unknown intent is not a request to run")
	}
}

func TestTheIntentToRunSurvivesBeingReadBack(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}

	running, known, err := s.DiscoveryIntent()
	if err != nil {
		t.Fatal(err)
	}
	if !known || !running {
		t.Errorf("the operator asked for a crawl; got running=%v known=%v", running, known)
	}
}

// Pausing is a decision too, and it has to outlive the process that took it. A restart that
// resumed a paused crawl would override the operator rather than remember them.
func TestPausingIsRememberedRatherThanForgotten(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDiscoveryIntent(false); err != nil {
		t.Fatal(err)
	}

	running, known, err := s.DiscoveryIntent()
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("a pause is an answer, so the intent is known")
	}
	if running {
		t.Error("the operator paused, and a restart must not undo that")
	}
}

// One fact about the installation, not a log of it.
func TestTheIntentIsOneRow(t *testing.T) {
	s := newTestStore(t)
	for _, want := range []bool{true, false, true} {
		if err := s.SetDiscoveryIntent(want); err != nil {
			t.Fatal(err)
		}
	}

	var rows int
	if err := s.DB().QueryRow(`SELECT count(*) FROM abom_discovery_state`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("want one row however often the intent changes; got %d", rows)
	}
}
