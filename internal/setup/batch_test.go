package setup

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakePatch answers writes and, when told, changes what default setup reads afterwards.
type fakePatch struct {
	mu      sync.Mutex
	writes  []string
	answer  func(repository string) (PatchResult, error)
	applied func(repository string, body PatchBody)
}

func (f *fakePatch) Write(_ context.Context, repository, _ string, _ []string, body PatchBody) (PatchResult, error) {
	f.mu.Lock()
	f.writes = append(f.writes, repository)
	f.mu.Unlock()
	res, err := f.answer(repository)
	if err == nil && res.Status < 300 && f.applied != nil {
		f.applied(repository, body)
	}
	return res, err
}

// setups answers each repository's default setup from a map a test can change between reads.
type setups struct {
	fakeCatalog
	mu   sync.Mutex
	held map[string]catalog.DefaultSetup
}

func (s *setups) DefaultSetup(_ context.Context, repository string) (catalog.DefaultSetup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[repository], nil
}

func (s *setups) set(repository string, ds catalog.DefaultSetup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[repository] = ds
}

// batchFixture is octo/<names> owned, crawled, listed with workflows and configured as given, a
// preview of them approved, and a batch started from it.
func batchFixture(t *testing.T, held map[string]catalog.DefaultSetup) (*store.Store, *setups, int64) {
	t.Helper()
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}
	slices.Sort(names)
	s := ownedStore(t, names...)
	yes, no := true, false
	facts := []store.ListedFacts{}
	for _, name := range names {
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at)
		                           VALUES (?, '', 0, 'done', '2026-10-05T00:00:00Z')`, name); err != nil {
			t.Fatal(err)
		}
		facts = append(facts, store.ListedFacts{Repository: name, HasWorkflows: &yes, HasActionsDir: &no, HasRootAction: &no})
	}
	if err := s.RecordListedFacts(facts, readAt); err != nil {
		t.Fatal(err)
	}
	cat := &setups{held: held}
	cat.actions = catalog.ActionsPermissions{Enabled: true, AllowedActions: "all"}
	r := Reader{Store: s, Catalog: cat, Now: func() time.Time { return readAt }}
	for _, name := range names {
		if err := r.Read(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Preview(s, PreviewRequest{CreatedBy: "test"}, func(string) bool { return true }, readAt)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if err := s.ApproveSetupPreview(p.ID, "data-douser", readAt); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartSetupBatch(p.ID, "test", readAt)
	if err != nil {
		t.Fatal(err)
	}
	return s, cat, batch
}

func runner(s *store.Store, cat *setups, patch *fakePatch) *Runner {
	clock := readAt
	var mu sync.Mutex
	return &Runner{Store: s, Reader: Reader{Store: s, Catalog: cat}, Patch: patch, InFlight: 10,
		VerifyAfter: time.Minute, VerifyFor: 10 * time.Minute, BreakAfter: 2,
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
		Sleep: func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(d)
			return nil
		}}
}

func steps(t *testing.T, s *store.Store, batch int64, repository string) []string {
	t.Helper()
	ledger, err := s.SetupLedger(repository, batch)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, w := range ledger {
		out = append(out, w.Step)
	}
	return out
}

func batchState(t *testing.T, s *store.Store, batch int64) store.SetupBatch {
	t.Helper()
	b, ok, err := s.SetupBatchOf(batch)
	if err != nil || !ok {
		t.Fatalf("SetupBatchOf: %v %v", ok, err)
	}
	return b
}

func accepted(string) (PatchResult, error) {
	return PatchResult{Status: http.StatusAccepted, RunID: 1}, nil
}

func TestABatchWritesEachApprovedRowAndReadsItBack(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/add":    {State: "configured", Languages: []string{"python"}, QuerySuite: "default"},
		"octo/enable": {State: "not-configured", Languages: []string{"actions", "python"}},
	})
	patch := &fakePatch{answer: accepted, applied: func(repository string, body PatchBody) {
		cat.set(repository, catalog.DefaultSetup{State: "configured", Languages: body.Languages})
	}}
	if err := runner(s, cat, patch).Run(context.Background(), batch); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(patch.writes, []string{"octo/add", "octo/enable"}) {
		t.Errorf("wrote %v, want each approved row once", patch.writes)
	}
	for _, repo := range []string{"octo/add", "octo/enable"} {
		if got := steps(t, s, batch, repo); !slices.Equal(got, []string{store.SetupStepSent, store.SetupStepVerified}) {
			t.Errorf("%s ledger %v, want sent then verified", repo, got)
		}
	}
	if b := batchState(t, s, batch); b.State != store.SetupBatchDone {
		t.Errorf("batch is %s (%s), want done", b.State, b.Reason)
	}
}

func TestAWriteThatLostALanguageStopsTheBatch(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/a": {State: "configured", Languages: []string{"python"}},
		"octo/b": {State: "configured", Languages: []string{"go"}},
	})
	patch := &fakePatch{answer: accepted, applied: func(repository string, _ PatchBody) {
		cat.set(repository, catalog.DefaultSetup{State: "configured", Languages: []string{"actions"}})
	}}
	r := runner(s, cat, patch)
	r.InFlight = 1
	if err := r.Run(context.Background(), batch); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b := batchState(t, s, batch)
	if b.State != store.SetupBatchStopped || !strings.Contains(b.Reason, "python") {
		t.Errorf("batch is %s (%q), want stopped naming the lost language", b.State, b.Reason)
	}
	if !slices.Equal(patch.writes, []string{"octo/a"}) {
		t.Errorf("wrote %v after a language was lost, want nothing after octo/a", patch.writes)
	}
}

func TestARowThatChangedSinceItsPreviewIsSkippedUnwritten(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/app": {State: "configured", Languages: []string{"python"}},
	})
	cat.set("octo/app", catalog.DefaultSetup{State: "configured", Languages: []string{"python", "go"}})
	patch := &fakePatch{answer: accepted}
	if err := runner(s, cat, patch).Run(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if len(patch.writes) != 0 {
		t.Errorf("wrote %v", patch.writes)
	}
	ledger, _ := s.SetupLedger("octo/app", batch)
	if len(ledger) != 1 || ledger[0].Step != store.SetupStepSkipped || !strings.Contains(ledger[0].Detail, "changed since") {
		t.Errorf("ledger %+v, want one skip saying it changed since the preview", ledger)
	}
}

func TestARepositoryOutsideTheTargetsIsRefusedAndRecorded(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/app": {State: "not-configured", Languages: []string{"actions"}},
	})
	patch := &fakePatch{answer: func(repository string) (PatchResult, error) {
		return PatchResult{}, &ErrNotATarget{Repository: repository}
	}}
	if err := runner(s, cat, patch).Run(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if got := steps(t, s, batch, "octo/app"); !slices.Equal(got, []string{store.SetupStepRefused}) {
		t.Errorf("ledger %v, want refused", got)
	}
}

func TestFailuresInARowPauseTheBatch(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/a": {State: "not-configured", Languages: []string{"actions"}},
		"octo/b": {State: "not-configured", Languages: []string{"actions"}},
		"octo/c": {State: "not-configured", Languages: []string{"actions"}},
	})
	patch := &fakePatch{answer: func(string) (PatchResult, error) {
		return PatchResult{Status: http.StatusUnprocessableEntity, Message: "Repository is not in a valid state"}, nil
	}}
	if err := runner(s, cat, patch).Run(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	b := batchState(t, s, batch)
	if b.State != store.SetupBatchPaused || len(patch.writes) != 2 {
		t.Errorf("batch %s (%q) after %d writes, want paused after 2", b.State, b.Reason, len(patch.writes))
	}
}

// A conflict means a setup run is under way; the row waits for it, and is given up after two.
func TestAConflictWaitsAndIsGivenUpAfterTwo(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/app": {State: "not-configured", Languages: []string{"actions"}},
	})
	patch := &fakePatch{answer: func(string) (PatchResult, error) {
		return PatchResult{Status: http.StatusConflict, Message: "validation run in progress"}, nil
	}}
	r := runner(s, cat, patch)
	r.BreakAfter = 5
	if err := r.Run(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	want := []string{store.SetupStepDeferred, store.SetupStepDeferred, store.SetupStepFailed}
	if got := steps(t, s, batch, "octo/app"); !slices.Equal(got, want) || len(patch.writes) != 3 {
		t.Errorf("ledger %v after %d writes, want %v", got, len(patch.writes), want)
	}
}

func TestTheInFlightCeilingHoldsTheNextWriteUntilOneIsReadBack(t *testing.T) {
	s, cat, batch := batchFixture(t, map[string]catalog.DefaultSetup{
		"octo/a": {State: "not-configured", Languages: []string{"actions"}},
		"octo/b": {State: "not-configured", Languages: []string{"actions"}},
	})
	var order []string
	patch := &fakePatch{answer: accepted, applied: func(repository string, body PatchBody) {
		order = append(order, "wrote "+repository)
		cat.set(repository, catalog.DefaultSetup{State: "configured", Languages: body.Languages})
	}}
	r := runner(s, cat, patch)
	r.InFlight = 1
	if err := r.Run(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	ledger, _ := s.SetupLedger("", batch)
	var seq []string
	for _, w := range ledger {
		seq = append(seq, w.Repository+" "+w.Step)
	}
	want := []string{"octo/a sent", "octo/a verified", "octo/b sent", "octo/b verified"}
	if !slices.Equal(seq, want) {
		t.Errorf("ledger order %v, want %v", seq, want)
	}
}
