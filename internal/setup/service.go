package setup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Service is what a server offers for writing default setup: previews from any server, and
// approvals and batches only from one started with write targets.
type Service struct {
	Store  *store.Store
	Runner *Runner
	// Targets and Accounts are the server's write guard: repositories named one by one, and
	// accounts whose declared repositories may all be written. Neither means nothing is written.
	Targets  []string
	Accounts []string
	// Login names the write token's owner, who is recorded as approving.
	Login func(ctx context.Context) (string, error)

	mu      sync.Mutex
	base    context.Context
	running map[int64]bool
}

// Writes reports whether this server may write anything at all.
func (sv *Service) Writes() bool {
	return len(sv.Targets)+len(sv.Accounts) > 0 && sv.Login != nil && sv.Runner != nil
}

// Allowed is the guard the flags describe.
func (sv *Service) Allowed() func(string) bool { return Targets(sv.Targets, sv.Accounts) }

// Start readies the service to run batches under ctx. A batch a restart interrupted comes back
// paused: nobody is watching it, and a write nobody is watching is the one that goes wrong unseen.
func (sv *Service) Start(ctx context.Context) error {
	sv.mu.Lock()
	sv.base, sv.running = ctx, map[int64]bool{}
	sv.mu.Unlock()
	batches, err := sv.Store.SetupBatches()
	if err != nil {
		return err
	}
	for _, b := range batches {
		if b.State == store.SetupBatchRunning {
			if err := sv.Store.SetSetupBatchState(b.ID, store.SetupBatchPaused,
				"the server restarted while it ran; resume it to continue", time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Preview classifies what the request covers, as a batch from it would write.
func (sv *Service) Preview(req PreviewRequest) (store.SetupPreview, error) {
	req.Accounts = append(append([]string{}, sv.Accounts...), sv.Targets...)
	return Preview(sv.Store, req, sv.Allowed(), time.Now().UTC())
}

// Approve records the write token's owner as approving a preview.
func (sv *Service) Approve(ctx context.Context, preview int64) error {
	if !sv.Writes() {
		return fmt.Errorf("this server writes nothing, so it approves nothing; start it with " +
			"--enable-targets or --enable-accounts to write")
	}
	login, err := sv.Login(ctx)
	if err != nil {
		return err
	}
	return sv.Store.ApproveSetupPreview(preview, login, time.Now().UTC())
}

// StartBatch starts the one batch an approved preview may run.
func (sv *Service) StartBatch(preview int64, by string) (int64, error) {
	if !sv.Writes() {
		return 0, fmt.Errorf("this server writes nothing; start it with --enable-targets or --enable-accounts to write")
	}
	batch, err := sv.Store.StartSetupBatch(preview, by, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	sv.run(batch)
	return batch, nil
}

// Act pauses, resumes or stops a batch.
func (sv *Service) Act(batch int64, action string) error {
	now := time.Now().UTC()
	switch action {
	case "pause":
		return sv.Store.SetSetupBatchState(batch, store.SetupBatchPaused, "paused by the operator", now)
	case "stop":
		return sv.Store.SetSetupBatchState(batch, store.SetupBatchStopped, "stopped by the operator", now)
	case "resume":
		if !sv.Writes() {
			return fmt.Errorf("this server writes nothing; start it with --enable-targets or --enable-accounts to write")
		}
		if err := sv.Store.SetSetupBatchState(batch, store.SetupBatchRunning, "", now); err != nil {
			return err
		}
		sv.run(batch)
		return nil
	}
	return fmt.Errorf("a batch is paused, resumed or stopped, not %q", action)
}

// run works a batch in the background, at most once at a time.
func (sv *Service) run(batch int64) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.base == nil || sv.running[batch] {
		return
	}
	sv.running[batch] = true
	go func() {
		err := sv.Runner.Run(sv.base, batch)
		sv.mu.Lock()
		delete(sv.running, batch)
		sv.mu.Unlock()
		if err != nil && sv.base.Err() == nil {
			_ = sv.Store.SetSetupBatchState(batch, store.SetupBatchPaused, "stopped on an error: "+err.Error(), time.Now())
		}
	}()
}
