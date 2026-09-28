package lockverify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func serviceStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for repository, coverage := range map[string]string{
		"octo/locked": "covered", "octo/unlocked": "not_onboarded",
	} {
		if err := s.RecordLockReading(repository, "c-"+repository[5:], "reader",
			[]store.LockCoverage{{Coverage: coverage}}, store.LockGraph{}); err != nil {
			t.Fatalf("RecordLockReading: %v", err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha)
			VALUES (?, '', 'octo', ?, 0, 'done', '2026-09-27T00:00:00Z', ?)`,
			repository, repository[5:], "c-"+repository[5:]); err != nil {
			t.Fatalf("queueing: %v", err)
		}
	}
	return s
}

type fetched struct{ owner, repo, commit string }

func service(t *testing.T, s *store.Store, gh string, now time.Time) (*Service, *[]fetched) {
	t.Helper()
	var calls []fetched
	return &Service{
		Store:  s,
		Runner: Runner{Gh: gh, Timeout: time.Minute},
		Fetch: func(_ context.Context, dest, owner, repo, commit string) error {
			calls = append(calls, fetched{owner, repo, commit})
			tree := aTree(t)
			return os.CopyFS(dest, os.DirFS(tree))
		},
		Now: func() time.Time { return now },
	}, &calls
}

const passing = `{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[]}`

// A request verifies the commit the repository resolves to, and what the tool said is kept with
// who asked.
func TestARequestVerifiesTheCurrentCommitAndKeepsTheAnswer(t *testing.T) {
	s := serviceStore(t)
	gh, _ := fakeGh(t, passing, 0, "")
	svc, calls := service(t, s, gh, time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))

	got, err := svc.Request(context.Background(), "octo/locked", ModeNetwork, "operator")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != (fetched{"octo", "locked", "c-locked"}) {
		t.Errorf("fetched %+v, want octo/locked at c-locked", *calls)
	}
	if got.Outcome != OutcomePassed || got.Commit != "c-locked" || got.RequestedBy != "operator" {
		t.Errorf("recorded %+v", got)
	}
	kept, err := s.LockVerifications("octo/locked", 10)
	if err != nil || len(kept) != 1 {
		t.Errorf("kept %d verifications, %v; want 1", len(kept), err)
	}
}

// Without a lockfile at the current commit there is nothing to ask the tool about, and asking
// anyway would spend the budget on a question with no subject.
func TestARepositoryWithNoLockfileHasNothingToVerify(t *testing.T) {
	s := serviceStore(t)
	gh, _ := fakeGh(t, passing, 0, "")
	svc, calls := service(t, s, gh, time.Now())

	for _, repository := range []string{"octo/unlocked", "nobody/here"} {
		if _, err := svc.Request(context.Background(), repository, ModeLocal, "operator"); !errors.Is(err, ErrNothingToVerify) {
			t.Errorf("%s: err = %v, want ErrNothingToVerify", repository, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("fetched %+v for nothing", *calls)
	}
}

// A network verification is a round of requests to GitHub, so it is budgeted: one commit is not
// asked about twice within the hour, and the day has a ceiling. Refused rather than queued.
func TestANetworkVerificationIsBudgeted(t *testing.T) {
	s := serviceStore(t)
	gh, _ := fakeGh(t, passing, 0, "")
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	svc, calls := service(t, s, gh, now)

	if _, err := svc.Request(context.Background(), "octo/locked", ModeNetwork, "operator"); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	if _, err := svc.Request(context.Background(), "octo/locked", ModeNetwork, "operator"); !errors.Is(err, ErrTooSoon) {
		t.Errorf("a second request within the hour: err = %v, want ErrTooSoon", err)
	}
	if len(*calls) != 1 {
		t.Errorf("fetched %d times, want once", len(*calls))
	}

	for i := 0; i < NetworkPerDay; i++ {
		if _, err := s.RecordLockVerification(store.LockVerification{Repository: "octo/other",
			Commit: "x", Mode: ModeNetwork, Outcome: OutcomePassed, VerifiedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	later, _ := service(t, s, gh, now.Add(2*time.Hour))
	if _, err := later.Request(context.Background(), "octo/locked", ModeNetwork, "operator"); !errors.Is(err, ErrBudget) {
		t.Errorf("past the day's ceiling: err = %v, want ErrBudget", err)
	}
}

// A tree that cannot be fetched is recorded as an error, so the attempt and its reason are on
// record rather than lost.
func TestATreeThatCannotBeFetchedIsRecordedAsAnError(t *testing.T) {
	s := serviceStore(t)
	gh, _ := fakeGh(t, passing, 0, "")
	svc, _ := service(t, s, gh, time.Now())
	svc.Fetch = func(context.Context, string, string, string, string) error {
		return errors.New("repository not found")
	}

	got, err := svc.Request(context.Background(), "octo/locked", ModeLocal, "operator")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got.Outcome != OutcomeError || got.Error == "" {
		t.Errorf("recorded %+v, want an error saying the tree could not be fetched", got)
	}
}
