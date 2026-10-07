package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Patch is the one write, as Patcher makes it.
type Patch interface {
	Write(ctx context.Context, repository, state string, languages []string, body PatchBody) (PatchResult, error)
}

// Runner works an approved preview's batch one repository at a time, reading each write back
// before counting it done.
type Runner struct {
	Store  *store.Store
	Reader Reader
	Patch  Patch
	// Pace waits for the pool to allow the next write; nil writes as fast as the loop turns.
	Pace func(ctx context.Context) error
	// InFlight is how many setup runs may be outstanding at once, across batches.
	InFlight int
	// VerifyAfter is how long a write waits before it is read back; VerifyFor how long it has.
	VerifyAfter time.Duration
	VerifyFor   time.Duration
	// BreakAfter failures in a row pause the batch: one broken cause fails every write after it.
	BreakAfter int
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
}

// maxDeferrals is how often a conflicting setup run is waited for before the row is given up.
const maxDeferrals = 2

func (r *Runner) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now()
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Run works a batch until it is done, paused or stopped, or ctx ends.
func (r *Runner) Run(ctx context.Context, batch int64) error {
	failures, last := 0, ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := r.Store.SetupBatchState(batch)
		if err != nil || state != store.SetupBatchRunning {
			return err
		}
		stop, err := r.readBackDue(ctx, batch, &failures, &last)
		if err != nil {
			return err
		}
		if stop != "" {
			return r.end(batch, store.SetupBatchStopped, stop)
		}
		if r.BreakAfter > 0 && failures >= r.BreakAfter {
			return r.end(batch, store.SetupBatchPaused,
				fmt.Sprintf("%d writes in a row failed, the last %s; resume once the cause is fixed", failures, last))
		}
		ready, waiting, err := r.ready(batch)
		if err != nil {
			return err
		}
		flying, err := r.Store.SetupWritesInFlight()
		if err != nil {
			return err
		}
		mine := 0
		for _, w := range flying {
			if w.BatchID == batch {
				mine++
			}
		}
		if len(ready) == 0 {
			if waiting == 0 && mine == 0 {
				return r.end(batch, store.SetupBatchDone, "")
			}
			if err := r.sleep(ctx, r.VerifyAfter); err != nil {
				return err
			}
			continue
		}
		if r.InFlight > 0 && len(flying) >= r.InFlight {
			if err := r.sleep(ctx, r.VerifyAfter); err != nil {
				return err
			}
			continue
		}
		if r.Pace != nil {
			if err := r.Pace(ctx); err != nil {
				return err
			}
		}
		step, detail, stop, err := r.writeOne(ctx, batch, ready[0])
		if err != nil {
			return err
		}
		if stop != "" {
			return r.end(batch, store.SetupBatchStopped, stop)
		}
		switch step {
		case store.SetupStepFailed:
			failures, last = failures+1, ready[0].Repository+": "+detail
		case store.SetupStepSent:
			failures = 0
		}
	}
}

// ready lists the rows that may be written now, and counts those waiting out a conflict.
func (r *Runner) ready(batch int64) ([]store.SetupPreviewRow, int, error) {
	rows, err := r.Store.SetupBatchRemaining(batch)
	if err != nil {
		return nil, 0, err
	}
	deferred, err := r.Store.SetupDeferred(batch)
	if err != nil {
		return nil, 0, err
	}
	out, waiting := []store.SetupPreviewRow{}, 0
	for _, row := range rows {
		if d, ok := deferred[strings.ToLower(row.Repository)]; ok && r.now().Sub(d.At) < r.VerifyAfter {
			waiting++
			continue
		}
		out = append(out, row)
	}
	return out, waiting, nil
}

func (r *Runner) end(batch int64, state, reason string) error {
	return r.Store.SetSetupBatchState(batch, state, reason, r.now())
}

func (r *Runner) record(w store.SetupWrite) error {
	w.At = r.now()
	return r.Store.AppendSetupWrite(w)
}

// writeOne reads a row fresh, refuses it if anything changed since its preview, and writes it.
// It answers the step recorded, and a reason when the whole batch has to stop.
func (r *Runner) writeOne(ctx context.Context, batch int64, row store.SetupPreviewRow) (string, string, string, error) {
	repo := row.Repository
	note := func(step, detail string) (string, string, string, error) {
		return step, detail, "", r.record(store.SetupWrite{BatchID: batch, Repository: repo, Step: step, Detail: detail})
	}
	for _, read := range []func(context.Context, string) error{r.Reader.Read, r.Reader.ReadConfiguration, r.Reader.ReadActionsPolicy} {
		if err := read(ctx, repo); err != nil {
			return note(store.SetupStepSkipped, "the read before the write failed: "+err.Error())
		}
	}
	classes, err := r.Store.SetupClasses(repo)
	if err != nil || len(classes) != 1 {
		return note(store.SetupStepSkipped, fmt.Sprintf("its class could not be read again: %v", err))
	}
	c := classes[0]
	if c.Class != row.Class || !slices.Equal(WriteNames(c.Languages), WriteNames(row.Languages)) {
		return note(store.SetupStepSkipped, fmt.Sprintf("changed since its preview: now %s, %s", c.Class, c.Evidence))
	}
	body, err := Body(c)
	if err != nil {
		return note(store.SetupStepRefused, err.Error())
	}
	// Not configured, nothing is analyzed yet: the languages read then are GitHub's suggestion.
	before := []string{}
	if c.State == "configured" {
		before = c.Languages
	}
	if missing := Removes(before, body.Languages); len(missing) > 0 {
		return note(store.SetupStepRefused, "the write would remove "+strings.Join(missing, ", "))
	}
	attachment := strings.TrimSpace(c.AttachmentStatus + " " + c.ConfigurationName)
	sent, _ := json.Marshal(body)
	res, err := r.Patch.Write(ctx, repo, c.State, c.Languages, body)
	var notTarget *ErrNotATarget
	var changed *ErrChanged
	switch {
	case errors.As(err, &notTarget):
		return note(store.SetupStepRefused, err.Error())
	case errors.As(err, &changed):
		return note(store.SetupStepSkipped, "changed between the read and the write: "+err.Error())
	case err != nil:
		return note(store.SetupStepFailed, err.Error())
	}
	w := store.SetupWrite{BatchID: batch, Repository: repo, Before: before, Sent: string(sent), Status: res.Status,
		RunID: res.RunID, RunURL: res.RunURL, AttachmentBefore: attachment, Detail: res.Message}
	switch {
	case res.Status == http.StatusOK || res.Status == http.StatusAccepted:
		w.Step = store.SetupStepSent
	case res.Status == http.StatusConflict:
		deferred, err := r.Store.SetupDeferred(batch)
		if err != nil {
			return "", "", "", err
		}
		w.Step = store.SetupStepDeferred
		if deferred[strings.ToLower(repo)].Count >= maxDeferrals {
			w.Step, w.Detail = store.SetupStepFailed, "a setup run kept conflicting: "+res.Message
		}
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden && !repositoryRefusal(res.Message):
		w.Step = store.SetupStepFailed
		return w.Step, res.Message, fmt.Sprintf("the write token was refused at %s (%d %s); nothing more is written with it",
			repo, res.Status, res.Message), r.record(w)
	default:
		w.Step = store.SetupStepFailed
		w.Detail = fmt.Sprintf("%d %s", res.Status, res.Message)
	}
	return w.Step, w.Detail, "", r.record(w)
}

// repositoryRefusal is a 403 about the repository rather than the token: GitHub answers one for an
// archived repository and for one without Advanced Security.
func repositoryRefusal(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "archived") || strings.Contains(m, "advanced security")
}

// readBackDue reads back each write of the batch old enough to be read, and answers a reason to
// stop where one took away a language or moved the repository's configuration.
func (r *Runner) readBackDue(ctx context.Context, batch int64, failures *int, last *string) (string, error) {
	flying, err := r.Store.SetupWritesInFlight()
	if err != nil {
		return "", err
	}
	for _, w := range flying {
		age := r.now().Sub(w.At)
		if w.BatchID != batch || age < r.VerifyAfter {
			continue
		}
		if err := r.Reader.Read(ctx, w.Repository); err != nil {
			continue
		}
		_ = r.Reader.ReadConfiguration(ctx, w.Repository)
		classes, err := r.Store.SetupClasses(w.Repository)
		if err != nil || len(classes) != 1 {
			continue
		}
		c := classes[0]
		after := strings.TrimSpace(c.AttachmentStatus + " " + c.ConfigurationName)
		step := store.SetupWrite{BatchID: batch, Repository: w.Repository, After: c.Languages,
			AttachmentBefore: w.AttachmentBefore, AttachmentAfter: after}
		switch missing := Removes(w.Before, c.Languages); {
		case len(missing) > 0:
			step.Step, step.Detail = store.SetupStepFailed, "lost "+strings.Join(missing, ", ")
			return fmt.Sprintf("the write to %s lost %s; every write since is unverified until somebody looks",
				w.Repository, strings.Join(missing, ", ")), r.record(step)
		case w.AttachmentBefore != "" && after != w.AttachmentBefore:
			step.Step, step.Detail = store.SetupStepFailed, "its configuration went from "+w.AttachmentBefore+" to "+orNone(after, "none")
			return fmt.Sprintf("the write to %s changed its configuration from %s to %s", w.Repository,
				w.AttachmentBefore, orNone(after, "none")), r.record(step)
		case c.State == "configured" && slices.Contains(WriteNames(c.Languages), "actions"):
			step.Step = store.SetupStepVerified
			*failures = 0
		case age >= r.VerifyFor:
			step.Step = store.SetupStepFailed
			step.Detail = fmt.Sprintf("still without actions %s after the write", age.Round(time.Minute))
			*failures, *last = *failures+1, w.Repository+": "+step.Detail
		default:
			continue
		}
		if err := r.record(step); err != nil {
			return "", err
		}
	}
	return "", nil
}

func orNone(v, none string) string {
	if v == "" {
		return none
	}
	return v
}
