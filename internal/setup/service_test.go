package setup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func serviceFixture(t *testing.T, targets []string) (*Service, *store.Store, *setups, *fakePatch) {
	t.Helper()
	held := map[string]catalog.DefaultSetup{
		"octo/a": {State: "not-configured", Languages: []string{"actions"}},
		"octo/b": {State: "configured", Languages: []string{"python"}},
	}
	s, cat, _ := batchFixture(t, held)
	patch := &fakePatch{answer: accepted, applied: func(repository string, body PatchBody) {
		cat.set(repository, catalog.DefaultSetup{State: "configured", Languages: body.Languages})
	}}
	r := runner(s, cat, patch)
	sv := &Service{Store: s, Runner: r, Targets: targets,
		Login: func(context.Context) (string, error) { return "data-douser", nil }}
	return sv, s, cat, patch
}

func TestAServerWithoutTargetsPreviewsButNeverApproves(t *testing.T) {
	sv, _, _, _ := serviceFixture(t, nil)
	p, err := sv.Preview(PreviewRequest{CreatedBy: "interface"})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Counts[PreviewNotATarget] != 2 {
		t.Errorf("counts %v, want both candidates outside the targets", p.Counts)
	}
	if err := sv.Approve(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "--enable-targets") {
		t.Errorf("Approve = %v, want a refusal naming the flag that would allow it", err)
	}
}

func TestAnApprovedPreviewRunsItsBatchAndRecordsWhoApproved(t *testing.T) {
	sv, s, _, patch := serviceFixture(t, []string{"octo/a", "octo/b"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sv.Start(ctx)
	p, err := sv.Preview(PreviewRequest{CreatedBy: "interface"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sv.Approve(ctx, p.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	batch, err := sv.StartBatch(p.ID, "interface")
	if err != nil {
		t.Fatalf("StartBatch: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _, _ := s.SetupBatchOf(batch)
		if b.State == store.SetupBatchDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch still %s (%s) after 10s", b.State, b.Reason)
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _, _ := s.SetupPreviewOf(p.ID)
	if got.ApprovedBy != "data-douser" || len(patch.writes) != 2 {
		t.Errorf("approved by %q, wrote %v", got.ApprovedBy, patch.writes)
	}
}

func TestABatchARestartInterruptedComesBackPaused(t *testing.T) {
	sv, s, _, _ := serviceFixture(t, []string{"octo/a"})
	p, err := sv.Preview(PreviewRequest{CreatedBy: "interface"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveSetupPreview(p.ID, "data-douser", readAt); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartSetupBatch(p.ID, "interface", readAt)
	if err != nil {
		t.Fatal(err)
	}
	sv.Start(context.Background())
	b, _, _ := s.SetupBatchOf(batch)
	if b.State != store.SetupBatchPaused || !strings.Contains(b.Reason, "restart") {
		t.Errorf("after a restart the batch is %s (%q), want paused saying why", b.State, b.Reason)
	}
	if err := sv.Act(batch, "stop"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if b, _, _ := s.SetupBatchOf(batch); b.State != store.SetupBatchStopped {
		t.Errorf("after stop the batch is %s", b.State)
	}
	if err := sv.Act(batch, "resume"); err == nil {
		t.Error("a stopped batch resumed")
	}
}
