package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The hole this closes: nothing in the loop re-reads the organization, so a repository created
// after the last enumeration is invisible to it however long it runs.
func TestTheFirstCycleReadsTheOrganization(t *testing.T) {
	sup, _ := newSupervisor(t)
	var calls int
	sup.Enumerate = func(context.Context) (int, error) { calls++; return 0, nil }

	sup.cycle(context.Background())

	if calls != 1 {
		t.Errorf("nothing has been read yet, so the cycle must read; got %d calls", calls)
	}
}

// Re-reading every thirty seconds would spend an organization's budget on a listing that almost
// never changes between cycles.
func TestTheOrganizationIsNotReReadWithinItsPeriod(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.EnumerateEvery = time.Hour
	var calls int
	sup.Enumerate = func(context.Context) (int, error) { calls++; return 0, nil }

	sup.cycle(context.Background())
	sup.cycle(context.Background())

	if calls != 1 {
		t.Errorf("the period has not elapsed, so one read; got %d", calls)
	}
}

func TestTheOrganizationIsReadAgainOnceThePeriodElapses(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.EnumerateEvery = time.Hour
	var calls int
	sup.Enumerate = func(context.Context) (int, error) { calls++; return 0, nil }

	sup.cycle(context.Background())
	sup.LastEnumeratedAt = sup.now().Add(-2 * time.Hour)
	sup.cycle(context.Background())

	if calls != 2 {
		t.Errorf("the period has elapsed, so a second read; got %d", calls)
	}
}

// A build with no enumerator is every build before this one, and it has to keep expanding.
func TestACycleWithNoEnumeratorStillRuns(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Enumerate = nil

	sup.cycle(context.Background())

	if got := sup.Status().LastError; got != "" {
		t.Errorf("a build that cannot enumerate is not a failure; got %q", got)
	}
}

// An organization that could not be read is a coverage gap, and a cycle that hid it would report
// a stale graph as current. It must be visible, and it must not stop the analysis already queued.
func TestAFailedReadIsReportedAndDoesNotStopTheCycle(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Enumerate = func(context.Context) (int, error) {
		return 0, errors.New("github said no")
	}

	sup.cycle(context.Background())

	if got := sup.Status().LastError; got == "" {
		t.Error("a read that failed has to reach the reader")
	}
	// The period is still consumed, or a failing account is retried every thirty seconds forever.
	if sup.LastEnumeratedAt.IsZero() {
		t.Error("a failed attempt still counts as an attempt, or it repeats every cycle")
	}
}
