// Package authoringpool runs authoring sessions in a cluster, each as a Job of the authoring pool.
// At most a few work at once; the rest wait in the order they were asked for, and each is checked
// again as it begins. The coordinator keeps what a session's worker reports and lands what it
// changed, under the gate's own rules; the worker only runs it.
package authoringpool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/audit"
	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Workers run one session each, in the authoring pool: execution.Runner.RunSession in production.
type Workers interface {
	RunSession(ctx context.Context, root string, t execution.SessionTask, in execution.SessionInputs,
		secrets map[string]string, calls execution.SessionCalls) error
}

// The paths a press of Start on a request, and of Draft on a run, is decided on: a waiting session
// begins only on a decision that allowed a session on its own request or run.
const (
	startPath = "/api/query-requests/%d/sessions"
	draftPath = "/api/runs/%d/drafts"
)

// Pool runs the sessions people start in a cluster.
type Pool struct {
	Store   store.Backend
	Workers Workers
	// Workspace is the coordinator's pack, which a session's worker unpacks at the same path and
	// what it changed lands in.
	Workspace string
	Model     string
	Limit     time.Duration
	Credits   float64
	// Concurrency is how many sessions work at once; fewer than one is one.
	Concurrency int
	// Mint mints the token one session's agent works with, and says when it expires.
	Mint func(ctx context.Context) (string, time.Time, error)
	// Revoke ends a session's token once its worker is done, rather than leaving it to expire; nil
	// leaves it to expire.
	Revoke func(ctx context.Context, token string) error
	Now    func() time.Time

	mu sync.Mutex
	// running is the sessions working, by session.
	running map[int64]*work
	// landing lands one session at a time, each checked against the pack as the last left it.
	landing sync.Mutex
}

// work is one session the pool began: on a request, or drafting from a run.
type work struct {
	session, request, run int64
	by, decision          string
	cancel                context.CancelFunc
	// stop closes when somebody stops the session, which its worker then reports.
	stop    chan struct{}
	stopped bool
	// inflight are the calls the worker last reported under way, and end how it reported the session
	// ended, nil until it did.
	inflight []harness.Flight
	end      *store.SessionEnd
}

func (p *Pool) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Start asks for a session on a request, kept with the digest of the signed decision that asked for
// it. It begins now if fewer than the limit are working, and otherwise waits its turn.
func (p *Pool) Start(request int64, by, decision string) (store.AuthoringSession, error) {
	id, err := p.Store.QueueAuthoringSession(request, by, p.Model, decision)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	p.schedule()
	sessions, err := p.Store.AuthoringSessions(request)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	for _, a := range sessions {
		if a.RowID == id {
			return a, nil
		}
	}
	return store.AuthoringSession{}, fmt.Errorf("session %d is not on record", id)
}

// Resume takes up the sessions that were waiting when the coordinator last stopped.
func (p *Pool) Resume() { p.schedule() }

// schedule begins waiting sessions, the longest waiting first, while fewer than the limit work.
func (p *Pool) schedule() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running == nil {
		p.running = map[int64]*work{}
	}
	for len(p.running) < max(1, p.Concurrency) {
		waiting, err := p.Store.QueuedAuthoringSessions()
		if err != nil {
			slog.Error("authoring: reading the sessions waiting", "error", err)
			return
		}
		if len(waiting) == 0 {
			return
		}
		next := waiting[0]
		decision, state, why := p.recheck(next)
		if why != "" {
			if err := p.Store.EndAuthoringSession(next.RowID, state, why); err != nil {
				slog.Error("authoring: ending a session that may not begin", "session", next.RowID, "error", err)
				return
			}
			continue
		}
		if err := p.Store.BeginQueuedSession(next.RowID); err != nil {
			slog.Error("authoring: beginning a session", "session", next.RowID, "error", err)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		w := &work{session: next.RowID, request: next.RequestID, run: next.RunID, by: next.StartedBy,
			decision: decision, cancel: cancel, stop: make(chan struct{})}
		p.running[w.session] = w
		go p.work(ctx, w)
	}
}

// recheck reads a waiting session's request or run, and its decision, as it is about to begin,
// answering the decision's digest, or how the session ends instead and why.
func (p *Pool) recheck(q store.QueuedSession) (decision, state, why string) {
	if q.RunID != 0 {
		if _, err := p.Store.AnalysisRun(q.RunID); err != nil {
			return "", store.SessionFailed, fmt.Sprintf("run %d is no longer on record", q.RunID)
		}
		return p.decided(q.RowID, fmt.Sprintf(draftPath, q.RunID), "run")
	}
	a := q.AuthoringSession
	requests, err := p.Store.QueryRequests()
	if err != nil {
		return "", store.SessionFailed, "reading its request: " + err.Error()
	}
	open := false
	for _, r := range requests {
		if r.RowID != a.RequestID {
			continue
		}
		if r.Status == store.RequestDone || r.Status == store.RequestDeclined {
			return "", store.SessionStopped, fmt.Sprintf("request %d is %s, so the session waiting on it did not begin",
				r.RowID, r.Status)
		}
		open = true
	}
	if !open {
		return "", store.SessionFailed, fmt.Sprintf("request %d is no longer on record", a.RequestID)
	}
	return p.decided(a.RowID, fmt.Sprintf(startPath, a.RequestID), "request")
}

// decided verifies the signed decision that asked for a waiting session as one allowing it on path,
// answering its digest, or how the session ends instead and why; what names what path is about.
func (p *Pool) decided(session int64, path, what string) (decision, state, why string) {
	decision, err := p.Store.SessionDecision(session)
	if err != nil {
		return "", store.SessionFailed, err.Error()
	}
	d, err := p.Store.VerifiedDecision(decision)
	if err != nil {
		return "", store.SessionFailed, "the decision that asked for it does not verify: " + err.Error()
	}
	var asked struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(d.Evidence(), &asked)
	switch {
	case d.Operation() != authz.OpAuthoring || !d.Allowed():
		return "", store.SessionFailed, "the decision that asked for it did not allow a session"
	case asked.Path != path:
		return "", store.SessionFailed, "the decision that asked for it allowed a session on another " + what
	}
	return decision, "", ""
}

// work runs one begun session to its end, and begins the next waiting.
func (p *Pool) work(ctx context.Context, w *work) {
	state, detail := p.run(ctx, w)
	p.mu.Lock()
	delete(p.running, w.session)
	p.mu.Unlock()
	w.cancel()
	if err := p.Store.EndAuthoringSession(w.session, state, detail); err != nil {
		slog.Error("authoring: ending a session", "session", w.session, "error", err)
	}
	p.schedule()
}

// run grants a session's worker what it reads and the token its agent works with, and keeps what it
// reports and lands what it changed, answering how the session ends.
func (p *Pool) run(ctx context.Context, w *work) (string, string) {
	failed := func(format string, a ...any) (string, string) {
		return store.SessionFailed, audit.Text(fmt.Sprintf(format, a...))
	}
	snap, err := p.Store.AuthoringSnapshot(w.session)
	if err != nil {
		return failed("reading what the session reads: %v", err)
	}
	snapshot, err := json.Marshal(snap)
	if err != nil {
		return failed("writing what the session reads: %v", err)
	}
	in := execution.SessionInputs{Snapshot: snapshot}
	if w.run != 0 {
		var state, why string
		if in.Databases, state, why = draftDatabases(snap); why != "" {
			return state, audit.Text(why)
		}
	}
	token, expires, err := p.Mint(ctx)
	if err != nil {
		return failed("minting the session's Copilot token: %v", err)
	}
	defer p.revoke(ctx, w.session, token)
	limit := p.Limit
	if until := expires.Sub(p.now()) - harness.TokenMargin; until < limit {
		limit = until
	}
	if limit < time.Second {
		return failed("the Copilot token minted for the session ends before the session could work")
	}
	connection := make([]byte, 24)
	if _, err := rand.Read(connection); err != nil {
		return failed("making the token the session's agent admits its worker with: %v", err)
	}
	task := execution.SessionTask{Session: w.session, Request: w.request, Run: w.run, By: w.by, Model: p.Model,
		Workspace: p.Workspace, LimitSeconds: int64(limit / time.Second), Credits: p.Credits, Decision: w.decision}
	secrets := map[string]string{execution.CopilotTokenSecret: token,
		execution.AgentConnectionSecret: hex.EncodeToString(connection)}
	err = p.Workers.RunSession(ctx, p.Workspace, task, in, secrets, execution.SessionCalls{
		Began: func(attempt, grant string) error { return p.Store.SetSessionAttempt(w.session, attempt, grant) },
		Keep:  func(r execution.Reported) error { return p.keep(w, r) },
		Land:  func(l execution.SessionLanding) error { return p.land(w, l) },
		Stop:  w.stop,
	})
	p.mu.Lock()
	stopped, end := w.stopped, w.end
	p.mu.Unlock()
	switch {
	case err != nil && stopped:
		return store.SessionStopped, audit.Text("stopped while it worked, and what it changed was not landed: " +
			err.Error())
	case err != nil:
		return failed("%v", err)
	case end == nil:
		return failed("its worker reported no end")
	}
	return end.State, end.Detail
}

// keep keeps one report of a session as its worker made it: what the session did, the calls it had
// under way, and how it ended once it says.
func (p *Pool) keep(w *work, r execution.Reported) error {
	end, inflight, err := Keep(p.Store, w.session, r)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	w.inflight = inflight
	if end != nil {
		w.end = end
	}
	return nil
}

// Keep replays one verified report of a worker's session into the store, answering how it says the
// session ended, if it does, and the calls it had under way.
func Keep(st store.Backend, session int64, r execution.Reported) (*store.SessionEnd, []harness.Flight, error) {
	var body harness.Reported
	if err := json.Unmarshal(r.Body, &body); err != nil {
		return nil, nil, fmt.Errorf("reading report %d: %w", r.Seq, err)
	}
	end, err := st.IngestAuthoringReport(session, store.AuthoringReport{Seq: r.Seq, Digest: r.Digest, Prev: r.Prev,
		Entries: body.Entries})
	return end, body.InFlight, err
}

// draftDatabases are what a draft reads of each picked repository's database, as the run kept it: a
// run that no longer keeps one ends the draft stopped, and one that cannot be handed over fails it.
func draftDatabases(snap store.AuthoringSnapshot) (map[string][]byte, string, string) {
	if snap.Run == nil || len(snap.Picks) == 0 {
		return nil, store.SessionFailed, "the session names no run and picks to draft from"
	}
	kept := map[string]string{}
	for _, m := range snap.Run.Members {
		kept[m.Repository] = m.DatabasePath
	}
	where := map[string]string{}
	for _, pick := range snap.Picks {
		db := kept[pick.Repository]
		if db == "" {
			return nil, store.SessionFailed, fmt.Sprintf("run %d holds no %s", snap.Run.RowID, pick.Repository)
		}
		where[pick.Repository] = db
	}
	databases, err := execution.DraftDatabases(where)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, store.SessionStopped, fmt.Sprintf("a database run %d kept is gone, so there is nothing to draft "+
			"from: %v", snap.Run.RowID, err)
	case err != nil:
		return nil, store.SessionFailed, err.Error()
	}
	return databases, "", ""
}

// land lands what a session changed, every change checked again against the gate's rules and the pack
// as the last landing left it. A draft changes nothing: what it drafted came in its reports.
func (p *Pool) land(w *work, l execution.SessionLanding) error {
	if w.run != 0 {
		if n := len(l.Manifest.Changed); n > 0 {
			return fmt.Errorf("a draft changes no file, and its worker says this one changed %d", n)
		}
		return nil
	}
	target, err := p.target(w.request)
	if err != nil {
		return err
	}
	changes := make([]harness.Change, 0, len(l.Manifest.Changed))
	for _, c := range l.Manifest.Changed {
		changes = append(changes, harness.Change{Path: c.Path, Before: c.Before, After: c.After, Body: l.Files[c.Path]})
	}
	p.landing.Lock()
	defer p.landing.Unlock()
	tests, err := harness.Land(p.Workspace, target, changes)
	if err != nil {
		return fmt.Errorf("landing what the session changed: %w", err)
	}
	if tests == "" {
		return nil
	}
	if err := execution.LandBeside(p.Workspace, tests, l.Actuals, l.Databases); err != nil {
		return fmt.Errorf("landing what the session's tests left: %w", err)
	}
	return nil
}

// revoke ends a session's token, however the session ended: whatever held it is gone.
func (p *Pool) revoke(ctx context.Context, session int64, token string) {
	if p.Revoke == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := p.Revoke(ctx, token); err != nil {
		slog.Warn("authoring: revoking a session's Copilot token; it ends when it expires", "session", session,
			"error", err)
	}
}

// target is the query a request is about now.
func (p *Pool) target(request int64) (string, error) {
	requests, err := p.Store.QueryRequests()
	if err != nil {
		return "", err
	}
	for _, r := range requests {
		if r.RowID == request {
			return r.QueryID, nil
		}
	}
	return "", fmt.Errorf("request %d is no longer on record", request)
}

// Stop ends the session on a request: one waiting ends unstarted, and one working is asked to stop,
// which its worker reports, landing what it changed, as a session stopped in the single binary keeps
// what it wrote. It reports whether there was one.
func (p *Pool) Stop(request int64) bool {
	if p.stopWorking(func(w *work) bool { return w.request == request }) {
		return true
	}
	sessions, err := p.Store.AuthoringSessions(request)
	if err != nil {
		return false
	}
	return p.stopWaiting(func(yield func(store.AuthoringSession) bool) {
		for _, a := range sessions {
			if !yield(a) {
				return
			}
		}
	})
}

// StopDraft ends the session drafting from a run as Stop ends one on a request.
func (p *Pool) StopDraft(run int64) bool {
	if p.stopWorking(func(w *work) bool { return w.run == run }) {
		return true
	}
	drafts, err := p.Store.DraftSessions(run)
	if err != nil {
		return false
	}
	return p.stopWaiting(func(yield func(store.AuthoringSession) bool) {
		for _, d := range drafts {
			if !yield(d.AuthoringSession) {
				return
			}
		}
	})
}

// stopWorking asks the working session that is, if one is, to stop, and reports whether there was one.
func (p *Pool) stopWorking(is func(*work) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.running {
		if is(w) {
			if !w.stopped {
				w.stopped = true
				close(w.stop)
			}
			return true
		}
	}
	return false
}

// stopWaiting ends the first of sessions that is waiting, unstarted, and reports whether there was one.
func (p *Pool) stopWaiting(sessions func(yield func(store.AuthoringSession) bool)) bool {
	for a := range sessions {
		if a.State == store.SessionQueued {
			return p.Store.EndAuthoringSession(a.RowID, store.SessionStopped, "stopped while it waited, so it never began") == nil
		}
	}
	return false
}

// InFlight is the calls a request's session had under way when its worker last reported.
func (p *Pool) InFlight(request int64) []harness.Flight {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.running {
		if w.request == request {
			return slices.Clone(w.inflight)
		}
	}
	return nil
}

// Reader reads what a worker reported for an attempt after a given report: execution.Runner.Reports.
type Reader interface {
	Reports(ctx context.Context, attempt, grant string, after int64, head string,
		keep func(execution.Reported) error) error
}

// Recover keeps what workers reported for the sessions a coordinator was running when it stopped,
// before a restart marks them interrupted, so their record is as whole as their workers made it. It
// answers how many reports it kept; a session whose reports cannot be read or verified keeps those
// before, and the rest are left.
func Recover(ctx context.Context, st store.Backend, r Reader) (int, error) {
	sessions, err := st.RunningRemoteSessions()
	if err != nil {
		return 0, err
	}
	kept := 0
	var errs []error
	for _, s := range sessions {
		err := r.Reports(ctx, s.Attempt, s.Grant, s.JournalSeq, s.JournalHead, func(rep execution.Reported) error {
			if _, _, err := Keep(st, s.Session, rep); err != nil {
				return err
			}
			kept++
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("session %d: %w", s.Session, err))
		}
	}
	return kept, errors.Join(errs...)
}

// Draft asks for a session drafting a request about queryID from the results picked out of run, kept
// with the picks and the digest of the signed decision that asked for it. It begins now if fewer than
// the limit are working, and otherwise waits its turn with the sessions on requests.
func (p *Pool) Draft(run int64, queryID string, picked []harness.Picked, by, decision string) (store.AuthoringSession, error) {
	picks := make([]store.DraftPick, 0, len(picked))
	for _, pick := range picked {
		picks = append(picks, store.DraftPick(pick))
	}
	id, err := p.Store.QueueDraftSession(run, queryID, by, p.Model, decision, picks)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	p.schedule()
	drafts, err := p.Store.DraftSessions(run)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	for _, d := range drafts {
		if d.RowID == id {
			return d.AuthoringSession, nil
		}
	}
	return store.AuthoringSession{}, fmt.Errorf("session %d is not on record", id)
}
