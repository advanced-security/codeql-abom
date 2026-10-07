package supervisor

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

// The hole this closes: nothing in the loop re-reads the organization, so a repository created
// after the last enumeration is invisible to it however long it runs.
func TestTheFirstCycleReadsTheOrganization(t *testing.T) {
	sup, _ := newSupervisor(t)
	var calls int
	sup.Enumerate = func(context.Context, Ask) (int, error) { calls++; return 0, nil }

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
	sup.Enumerate = func(context.Context, Ask) (int, error) { calls++; return 0, nil }

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
	sup.Enumerate = func(context.Context, Ask) (int, error) { calls++; return 0, nil }

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
	sup.Enumerate = func(context.Context, Ask) (int, error) {
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

// An event naming a repository no listing has named asks for its account, and waiting out the
// period left has-ghas's new repositories invisible to their own pushes. The full listing keeps
// its period, so asking for one account cannot postpone the others.
func TestAnAccountAnEventAsksForIsListedOnTheNextCycle(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.EnumerateEvery = time.Hour
	var calls []Ask
	sup.Enumerate = func(_ context.Context, ask Ask) (int, error) {
		calls = append(calls, ask)
		return 0, nil
	}

	sup.cycle(context.Background())
	listedAt := sup.LastEnumeratedAt
	sup.ListSoon("has-ghas")
	sup.ListSoon("Has-GHAS")
	sup.cycle(context.Background())
	sup.cycle(context.Background())

	if len(calls) != 2 || !calls[0].empty() || !slices.Equal(calls[1].Owners, []string{"has-ghas"}) ||
		len(calls[1].Full)+len(calls[1].Repositories) != 0 {
		t.Errorf("reads = %+v, want every account, then has-ghas once", calls)
	}
	if !sup.LastEnumeratedAt.Equal(listedAt) {
		t.Error("listing the account an event asked for moved the period of the full listing")
	}
}

// A due listing reads every account as its marks allow, which answers an ask to list one, but not
// an ask to list one whole or to read one repository: a sweep names neither.
func TestWhatADueListingCannotAnswerIsAskedBesideIt(t *testing.T) {
	sup, _ := newSupervisor(t)
	var calls []Ask
	sup.Enumerate = func(_ context.Context, ask Ask) (int, error) {
		calls = append(calls, ask)
		return 0, nil
	}
	sup.ListSoon("has-ghas")
	sup.ListFullySoon("acme")
	sup.ReadSoon("has-ghas/Test-Repo-3")
	sup.ReadSoon("has-ghas/test-repo-3")

	sup.cycle(context.Background())
	sup.cycle(context.Background())

	want := Ask{Full: []string{"acme"}, Repositories: []string{"has-ghas/Test-Repo-3"}}
	if len(calls) != 2 || !calls[0].empty() || !reflect.DeepEqual(calls[1], want) {
		t.Errorf("reads = %+v, want every account, then %+v once", calls, want)
	}
}

// A new Actions advisory changes exposure with no repository changing, and the next refresh is up
// to a day away. The floor still holds: an event cannot make the loop refetch every minute.
func TestAnAdvisoryEventRefreshesOnceTheFloorAllows(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.AdvisoryMaxAge = 24 * time.Hour
	sup.AdvisoryMinInterval = 15 * time.Minute
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Minute)
	var asked int
	sup.Advisor = func(context.Context) (int, error) {
		asked++
		return 0, nil
	}

	sup.RefreshAdvisoriesSoon()
	sup.cycle(context.Background())
	if asked != 0 {
		t.Fatalf("refreshed %d times inside the floor", asked)
	}
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Hour)
	sup.cycle(context.Background())
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Hour)
	sup.cycle(context.Background())

	if asked != 1 {
		t.Errorf("refreshed %d times, want once past the floor and not again for the same ask", asked)
	}
}
