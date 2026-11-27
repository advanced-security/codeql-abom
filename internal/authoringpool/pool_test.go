package authoringpool

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// workers stand in for the authoring pool's Jobs: each session works until the test says what its
// worker reported and left.
type workers struct {
	mu      sync.Mutex
	tasks   []execution.SessionTask
	inputs  []execution.SessionInputs
	secrets []map[string]string
	left    map[int64]chan left
	began   chan int64
	stops   chan int64
}

// left is what a worker does next: keep reports, then keep working if more, or end as err says, or
// land landing.
type left struct {
	reports []execution.Reported
	landing *execution.SessionLanding
	err     error
	more    bool
}

func newWorkers() *workers {
	return &workers{left: map[int64]chan left{}, began: make(chan int64, 16), stops: make(chan int64, 16)}
}

func (w *workers) gate(session int64) chan left {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.left[session] == nil {
		w.left[session] = make(chan left, 1)
	}
	return w.left[session]
}

func (w *workers) RunSession(ctx context.Context, _ string, t execution.SessionTask, in execution.SessionInputs,
	secrets map[string]string, calls execution.SessionCalls) error {
	w.mu.Lock()
	w.tasks, w.inputs, w.secrets = append(w.tasks, t), append(w.inputs, in), append(w.secrets, secrets)
	w.mu.Unlock()
	if err := calls.Began(fmt.Sprintf("attempt-%d", t.Session), "grant"); err != nil {
		return err
	}
	w.began <- t.Session
	stop := calls.Stop
	for {
		select {
		case l := <-w.gate(t.Session):
			for _, r := range l.reports {
				if err := calls.Keep(r); err != nil {
					return err
				}
			}
			switch {
			case l.more:
				continue
			case l.err != nil:
				return l.err
			case l.landing != nil:
				return calls.Land(*l.landing)
			}
			return nil
		case <-stop:
			stop = nil
			w.stops <- t.Session
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *workers) leave(session int64, l left) { w.gate(session) <- l }

// stopped waits for the worker of session to be asked to stop.
func (w *workers) stopped(t *testing.T, session int64) {
	t.Helper()
	select {
	case s := <-w.stops:
		if s != session {
			t.Fatalf("the worker of session %d was asked to stop, not %d", s, session)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the worker of session %d was never asked to stop", session)
	}
}

// report is a worker's report seq of a session, following the one before it.
func report(t *testing.T, seq int64, entries []store.AuthoringEntry, inflight ...harness.Flight) execution.Reported {
	t.Helper()
	body, err := json.Marshal(harness.Reported{Entries: entries, InFlight: inflight})
	if err != nil {
		t.Fatal(err)
	}
	r := execution.Reported{Seq: seq, Digest: fmt.Sprintf("r%d", seq), Body: body}
	if seq > 1 {
		r.Prev = fmt.Sprintf("r%d", seq-1)
	}
	return r
}

func (w *workers) next(t *testing.T) int64 {
	t.Helper()
	select {
	case s := <-w.began:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("no session began")
		return 0
	}
}

func (w *workers) idle(t *testing.T) {
	t.Helper()
	select {
	case s := <-w.began:
		t.Fatalf("session %d began beyond the limit", s)
	case <-time.After(100 * time.Millisecond):
	}
}

type bench struct {
	pool    *Pool
	store   *store.Store
	workers *workers
	pack    string
	mints   *int
	revoked *revocations
}

// revocations are the session tokens a pool revoked, in order.
type revocations struct {
	mu     sync.Mutex
	tokens []string
}

func (r *revocations) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.tokens)
}

const runnerQuery = "src/SelfHostedRunner.ql"

func queryText(id string) string {
	return "/**\n * @name SelfHostedRunner\n * @kind problem\n * @id " + id + "\n */\nimport actions\n\n" +
		"from Job j select j, \"x\"\n"
}

func newBench(t *testing.T, concurrency int) bench {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "abom.db"), store.Options{AuthzDir: filepath.Join(root, "authz")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(root, "pack")
	if _, err := customql.Scaffold(pack, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, filepath.FromSlash(runnerQuery)),
		[]byte(queryText("acme/self-hosted-runner")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := customql.ScaffoldTest(pack, "SelfHostedRunner"); err != nil {
		t.Fatal(err)
	}
	b := bench{store: st, workers: newWorkers(), pack: pack, mints: new(int), revoked: &revocations{}}
	b.pool = b.newPool(concurrency)
	return b
}

// newPool is a coordinator's pool over the bench's store, workers and pack.
func (b bench) newPool(concurrency int) *Pool {
	return &Pool{Store: b.store, Workers: b.workers, Workspace: b.pack, Model: "claude-opus-5",
		Limit: 45 * time.Minute, Credits: 500, Concurrency: concurrency,
		Mint: func(context.Context) (string, time.Time, error) {
			*b.mints++
			return "ghs_session", time.Now().Add(time.Hour), nil
		},
		Revoke: func(_ context.Context, token string) error {
			b.revoked.mu.Lock()
			defer b.revoked.mu.Unlock()
			b.revoked.tokens = append(b.revoked.tokens, token)
			return nil
		}}
}

// asked files a request and the signed decision a person's press of Start records for it.
func (b bench) asked(t *testing.T) (request int64, decision string) {
	t.Helper()
	request, err := b.store.SaveQueryRequest(store.QueryRequest{Description: "report a self-hosted runner",
		Compliant: "runs-on: ubuntu-latest", NonCompliant: "runs-on: self-hosted", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return request, b.decide(t, request, true)
}

func (b bench) decide(t *testing.T, request int64, allow bool) string {
	t.Helper()
	ctx := context.Background()
	point, err := b.store.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := point.Decide(ctx, authz.Request{Operation: authz.OpAuthoring, Principal: authz.Local(""),
		Subject: authz.RouteSubject("POST /api/query-requests/{id}/sessions"), Allow: allow, Reason: "decided",
		Evidence: map[string]string{"path": fmt.Sprintf("/api/query-requests/%d/sessions", request)}})
	if err != nil {
		t.Fatal(err)
	}
	return d.Digest()
}

func (b bench) start(t *testing.T, request int64, decision string) int64 {
	t.Helper()
	session, err := b.pool.Start(request, "operator", decision)
	if err != nil {
		t.Fatal(err)
	}
	return session.RowID
}

func (b bench) session(t *testing.T, request, session int64) store.AuthoringSession {
	t.Helper()
	sessions, err := b.store.AuthoringSessions(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range sessions {
		if a.RowID == session {
			return a
		}
	}
	t.Fatalf("session %d is not on request %d", session, request)
	return store.AuthoringSession{}
}

// over waits for a session to end, and answers how.
func (b bench) over(t *testing.T, request, session int64) store.AuthoringSession {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a := b.session(t, request, session)
		if a.State != store.SessionRunning && a.State != store.SessionQueued {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %d is still %s", session, a.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// worked is what a worker does for a session that decided to improve the query, changed query to what
// it says, and finished: its one report, and the change with the file it replaced. An empty query is
// a change outside the session's bounds instead.
func (b bench) worked(t *testing.T, session int64, query string) left {
	return b.ended(t, session, query, store.SessionFinished)
}

// ended is worked, ending the session in state.
func (b bench) ended(t *testing.T, session int64, query, state string) left {
	t.Helper()
	entries := []store.AuthoringEntry{
		{Kind: store.EntryTarget, Target: &store.TargetDecision{QueryID: "acme/self-hosted-runner", Rationale: "it fits"}},
		{Kind: store.EntryEvent, Event: &store.AuthoringEvent{SessionID: session, At: "2026-11-26T12:00:00Z",
			Hook: "pre-tool-use", Tool: "edit", Decision: "allowed"}},
		{Kind: store.EntryEnd, End: &store.SessionEnd{State: state, Detail: "done"}},
	}
	before, err := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery)))
	if err != nil {
		t.Fatal(err)
	}
	path := runnerQuery
	if query == "" {
		path, query = "src/Elsewhere.ql", "select 1\n"
	}
	l := execution.SessionLanding{Files: map[string][]byte{path: []byte(query)}}
	c := execution.Changed{Path: path, After: digest([]byte(query))}
	if path == runnerQuery {
		c.Before = digest(before)
	}
	l.Manifest.Changed = []execution.Changed{c}
	return left{reports: []execution.Reported{report(t, 1, entries)}, landing: &l}
}

// At most the limit of sessions work at once; the rest wait, unstarted, and begin in the order they
// were asked for as the ones working end.
func TestAtMostTheLimitWorkAtOnceAndTheRestWaitTheirTurn(t *testing.T) {
	b := newBench(t, 1)
	var requests, sessions []int64
	for range 3 {
		request, decision := b.asked(t)
		requests, sessions = append(requests, request), append(sessions, b.start(t, request, decision))
	}
	if got := b.workers.next(t); got != sessions[0] {
		t.Fatalf("session %d began first, not %d", got, sessions[0])
	}
	b.workers.idle(t)
	for i := 1; i < 3; i++ {
		if a := b.session(t, requests[i], sessions[i]); a.State != store.SessionQueued || a.Ahead != i-1 {
			t.Errorf("session %d reads %s with %d ahead; want it waiting behind %d", sessions[i], a.State, a.Ahead, i-1)
		}
	}
	for i := range 3 {
		b.workers.leave(sessions[i], b.worked(t, sessions[i], queryText("acme/self-hosted-runner")+"// "+fmt.Sprint(i)+"\n"))
		b.over(t, requests[i], sessions[i])
		if i < 2 {
			if got := b.workers.next(t); got != sessions[i+1] {
				t.Fatalf("session %d began next, not %d", got, sessions[i+1])
			}
		}
	}
}

// What a session's worker reports is kept, and what it changed lands in the coordinator's pack; the
// session ends as its worker said it did.
func TestASessionsReportIsKeptAndWhatItChangedLands(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	changed := queryText("acme/self-hosted-runner") + "// improved\n"
	b.workers.leave(session, b.worked(t, session, changed))

	if a := b.over(t, request, session); a.State != store.SessionFinished || a.Events != 1 {
		t.Errorf("the session ended %s with %d events; want finished with the one reported", a.State, a.Events)
	}
	if body, _ := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery))); string(body) != changed {
		t.Errorf("the query reads %q; want what the session left", body)
	}
	requests, err := b.store.QueryRequests()
	if err != nil || len(requests) != 1 || requests[0].QueryID != "acme/self-hosted-runner" {
		t.Errorf("the request reads %+v %v; want the target the session decided", requests, err)
	}
}

// A session whose changes cannot land ends failed, saying why; what it reported is kept, as the
// record of what it tried, and the pack is as it was.
func TestASessionWhoseChangesCannotLandEndsFailedWithItsReportKept(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	b.workers.leave(session, b.worked(t, session, ""))

	a := b.over(t, request, session)
	if a.State != store.SessionFailed || !strings.Contains(a.Detail, "src/Elsewhere.ql") || a.Events != 1 {
		t.Errorf("the session ended %s (%q) with %d events; want failed naming the file, its report kept",
			a.State, a.Detail, a.Events)
	}
	if _, err := os.Stat(filepath.Join(b.pack, "src", "Elsewhere.ql")); err == nil {
		t.Error("a change outside the session's bounds landed")
	}
}

// A waiting session is checked again as it begins: it begins only on a request still open, asked for
// by a decision that verifies and allowed a session on that request. One that fails either never
// works, and ends saying why.
func TestAWaitingSessionIsCheckedAgainAsItBegins(t *testing.T) {
	for name, tc := range map[string]struct {
		decision func(b bench, t *testing.T, request int64) string
		close    bool
		state    string
		why      string
	}{
		"its request was closed": {decision: func(b bench, t *testing.T, request int64) string {
			return b.decide(t, request, true)
		}, close: true, state: store.SessionStopped, why: "done"},
		"no decision asked for it": {decision: func(bench, *testing.T, int64) string { return "" },
			state: store.SessionFailed, why: "decision"},
		"its decision refused it": {decision: func(b bench, t *testing.T, request int64) string {
			return b.decide(t, request, false)
		}, state: store.SessionFailed, why: "decision"},
		"its decision was for another request": {decision: func(b bench, t *testing.T, request int64) string {
			return b.decide(t, request+100, true)
		}, state: store.SessionFailed, why: "another request"},
		"its decision was signed by a key nobody trusts": {decision: func(b bench, t *testing.T, request int64) string {
			signer, err := authz.OpenLocal(t.TempDir(), time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			forger := &authz.Point{Signer: signer, Ledger: b.store}
			d, err := forger.Decide(context.Background(), authz.Request{Operation: authz.OpAuthoring,
				Principal: authz.Local(""), Subject: authz.RouteSubject("POST /api/query-requests/{id}/sessions"),
				Allow: true, Evidence: map[string]string{"path": fmt.Sprintf("/api/query-requests/%d/sessions", request)}})
			if err != nil {
				t.Fatal(err)
			}
			return d.Digest()
		}, state: store.SessionFailed, why: "does not verify"},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBench(t, 1)
			first, decision := b.asked(t)
			working := b.start(t, first, decision)
			b.workers.next(t)
			request, _ := b.asked(t)
			waiting := b.start(t, request, tc.decision(b, t, request))
			if tc.close {
				requests, _ := b.store.QueryRequests()
				for _, r := range requests {
					if r.RowID == request {
						r.Status = store.RequestDone
						if _, err := b.store.SaveQueryRequest(r); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			b.workers.leave(working, b.worked(t, working, queryText("acme/self-hosted-runner")+"// 1\n"))
			if a := b.over(t, request, waiting); a.State != tc.state || !strings.Contains(a.Detail, tc.why) {
				t.Errorf("the waiting session ended %s (%q); want %s naming %q", a.State, a.Detail, tc.state, tc.why)
			}
			b.workers.idle(t)
		})
	}
}

// Stopping a waiting session ends it unstarted. Stopping a working one asks its worker to stop, and
// what it reports it changed lands, as a session stopped in the single binary keeps what it wrote.
func TestStoppingASessionLandsWhatItsWorkerReportsItChanged(t *testing.T) {
	b := newBench(t, 1)
	first, decision := b.asked(t)
	working := b.start(t, first, decision)
	b.workers.next(t)
	request, decision := b.asked(t)
	waiting := b.start(t, request, decision)

	if !b.pool.Stop(request) {
		t.Fatal("the waiting session could not be stopped")
	}
	if a := b.over(t, request, waiting); a.State != store.SessionStopped || a.StartedAt != "" {
		t.Errorf("the session stopped while waiting reads %+v", a)
	}
	stopped, again := b.pool.Stop(first), b.pool.Stop(first)
	if !stopped || !again {
		t.Fatal("the working session could not be stopped, or stopped twice")
	}
	b.workers.stopped(t, working)
	changed := queryText("acme/self-hosted-runner") + "// halfway\n"
	b.workers.leave(working, b.ended(t, working, changed, store.SessionStopped))
	if a := b.over(t, first, working); a.State != store.SessionStopped || a.Events != 1 {
		t.Errorf("the session stopped while working ended %s (%q) with %d events", a.State, a.Detail, a.Events)
	}
	if body, _ := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery))); string(body) != changed {
		t.Errorf("the query reads %q; want what the stopped session reported it changed", body)
	}
	if b.pool.Stop(first) {
		t.Error("a session that has ended was stopped again")
	}
}

// A worker stopped before it could say what its session changed, as one killed is, lands nothing, and
// what it reported before stays as the record of what it did.
func TestAStoppedWorkerThatReportsNoResultLandsNothing(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	b.workers.leave(session, left{more: true, reports: []execution.Reported{report(t, 1, []store.AuthoringEntry{
		{Kind: store.EntryEvent, Event: &store.AuthoringEvent{SessionID: session, At: "2026-11-26T12:00:00Z",
			Hook: "pre-tool-use", Tool: "view", Decision: "allowed"}}})}})
	b.pool.Stop(request)
	b.workers.stopped(t, session)
	b.workers.leave(session, left{err: errors.New("the worker for session: job failed: OOMKilled")})
	a := b.over(t, request, session)
	if a.State != store.SessionStopped || !strings.Contains(a.Detail, "not landed") || a.Events != 1 {
		t.Errorf("the killed session ended %s (%q) with %d events", a.State, a.Detail, a.Events)
	}
	if body, _ := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery))); string(body) != queryText("acme/self-hosted-runner") {
		t.Error("a killed session's changes landed")
	}
}

// A worker that lands what its session changed without ever reporting how the session ended fails it:
// the record would otherwise say it is still working.
func TestAWorkerThatReportsNoEndFailsTheSession(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	l := b.worked(t, session, queryText("acme/self-hosted-runner")+"// 1\n")
	var body harness.Reported
	if err := json.Unmarshal(l.reports[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	l.reports[0] = report(t, 1, body.Entries[:len(body.Entries)-1])
	b.workers.leave(session, l)
	if a := b.over(t, request, session); a.State != store.SessionFailed || !strings.Contains(a.Detail, "no end") {
		t.Errorf("the session ended %s (%q)", a.State, a.Detail)
	}
}

// A session's token is revoked once its worker is done, however the session ended, rather than living
// out its hour beside an agent that is gone; one that never began was minted none to revoke.
func TestASessionsTokenIsRevokedOnceItsWorkerIsDone(t *testing.T) {
	for name, end := range map[string]func(b bench, session int64){
		"finished": func(b bench, session int64) {
			b.workers.leave(session, b.worked(t, session, queryText("acme/self-hosted-runner")+"// 1\n"))
		},
		"failed": func(b bench, session int64) {
			b.workers.leave(session, left{err: errors.New("the worker for session: job failed")})
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBench(t, 1)
			request, decision := b.asked(t)
			session := b.start(t, request, decision)
			b.workers.next(t)
			if got := b.revoked.list(); len(got) != 0 {
				t.Fatalf("the token was revoked while the session worked: %v", got)
			}
			end(b, session)
			b.over(t, request, session)
			deadline := time.Now().Add(10 * time.Second)
			for len(b.revoked.list()) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := b.revoked.list(); !slices.Equal(got, []string{"ghs_session"}) {
				t.Errorf("revoked %v once the session %s; want its one token", got, name)
			}
		})
	}
}

// The calls a session has under way are the ones its worker last reported, while it works.
func TestTheCallsUnderWayAreTheOnesTheWorkerLastReported(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	flight := harness.Flight{Tool: "ql-mcp-codeql_test_run", Subject: "test/SelfHostedRunner", Step: "tests",
		Since: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	b.workers.leave(session, left{more: true, reports: []execution.Reported{report(t, 1, nil, flight)}})
	deadline := time.Now().Add(10 * time.Second)
	for got := b.pool.InFlight(request); len(got) != 1 || got[0] != flight; got = b.pool.InFlight(request) {
		if time.Now().After(deadline) {
			t.Fatalf("the calls under way read %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.workers.leave(session, b.worked(t, session, queryText("acme/self-hosted-runner")+"// 1\n"))
	b.over(t, request, session)
	if got := b.pool.InFlight(request); len(got) != 0 {
		t.Errorf("a session that has ended has calls under way: %+v", got)
	}
}

// A report the store will not keep stops the session there: it ends failed, and nothing it changed
// lands.
func TestAReportTheStoreRefusesFailsTheSession(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	session := b.start(t, request, decision)
	b.workers.next(t)
	l := b.worked(t, session, queryText("acme/self-hosted-runner")+"// 1\n")
	l.reports[0].Seq = 2
	b.workers.leave(session, l)
	if a := b.over(t, request, session); a.State != store.SessionFailed || a.Events != 0 {
		t.Errorf("the session ended %s (%q) with %d events", a.State, a.Detail, a.Events)
	}
	if body, _ := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery))); string(body) != queryText("acme/self-hosted-runner") {
		t.Error("a session whose report was refused landed what it changed")
	}
}

// A session's token is minted as it begins, not as it waits, and handed to its agent alone with the
// token its CLI admits the worker with; the session lasts no longer than the token, less the margin.
func TestASessionsTokenIsMintedAsItBeginsAndBoundsItsLength(t *testing.T) {
	b := newBench(t, 1)
	b.pool.Limit = 2 * time.Hour
	first, decision := b.asked(t)
	working := b.start(t, first, decision)
	b.workers.next(t)
	request, decision := b.asked(t)
	waiting := b.start(t, request, decision)
	if *b.mints != 1 {
		t.Errorf("%d tokens minted for one session working and one waiting", *b.mints)
	}
	b.workers.leave(working, b.worked(t, working, queryText("acme/self-hosted-runner")+"// 1\n"))
	b.workers.next(t)
	if *b.mints != 2 {
		t.Errorf("%d tokens minted once the waiting session began", *b.mints)
	}
	b.workers.mu.Lock()
	task, secrets := b.workers.tasks[1], b.workers.secrets[1]
	b.workers.mu.Unlock()
	if task.Session != waiting || task.Decision != decision || task.Credits != 500 ||
		task.LimitSeconds <= 0 || task.LimitSeconds > int64((55*time.Minute)/time.Second) {
		t.Errorf("the session was granted %+v; want its decision, its credits, and the token's length less the margin", task)
	}
	if secrets[execution.CopilotTokenSecret] != "ghs_session" || secrets[execution.AgentConnectionSecret] == "" ||
		len(secrets) != 2 {
		t.Errorf("the session's pod was given %d secrets: %v", len(secrets), secrets[execution.AgentConnectionSecret] != "")
	}
}

// A coordinator that restarts takes up the sessions that were waiting, in their turn.
func TestARestartedPoolTakesUpWhatWaited(t *testing.T) {
	b := newBench(t, 1)
	first, decision := b.asked(t)
	b.start(t, first, decision)
	b.workers.next(t)
	request, decision := b.asked(t)
	waiting := b.start(t, request, decision)
	if _, err := b.store.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	b.newPool(1).Resume()
	if got := b.workers.next(t); got != waiting {
		t.Errorf("session %d began after the restart, not the one waiting, %d", got, waiting)
	}
}

// reader stands in for the object store a restarted coordinator reads workers' reports from.
type reader map[string][]execution.Reported

func (r reader) Reports(_ context.Context, attempt, _ string, after int64, head string,
	keep func(execution.Reported) error) error {
	for _, rep := range r[attempt] {
		if rep.Seq <= after {
			continue
		}
		if rep.Prev != head {
			return errors.New("a report that does not follow the one kept before it")
		}
		if err := keep(rep); err != nil {
			return err
		}
		head = rep.Digest
	}
	return nil
}

// A coordinator that restarts keeps what each session's worker reported after the last report it had
// kept, before the session is marked interrupted; a session whose reports cannot be read keeps the
// ones before.
func TestARestartKeepsWhatWorkersReportedBeforeTheyAreInterrupted(t *testing.T) {
	b := newBench(t, 2)
	event := func(session int64, tool string) []store.AuthoringEntry {
		return []store.AuthoringEntry{{Kind: store.EntryEvent, Event: &store.AuthoringEvent{SessionID: session,
			At: "2026-11-26T12:00:00Z", Hook: "pre-tool-use", Tool: tool, Decision: "allowed"}}}
	}
	var sessions, requests []int64
	for range 2 {
		request, decision := b.asked(t)
		requests, sessions = append(requests, request), append(sessions, b.start(t, request, decision))
		b.workers.next(t)
	}
	for _, s := range sessions {
		b.workers.leave(s, left{more: true, reports: []execution.Reported{report(t, 1, event(s, "view"))}})
	}
	for i, s := range sessions {
		deadline := time.Now().Add(10 * time.Second)
		for b.session(t, requests[i], s).Events != 1 {
			if time.Now().After(deadline) {
				t.Fatal("the first reports were not kept")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	unread := report(t, 3, event(sessions[1], "edit"))
	unread.Prev = "elsewhere"
	kept, err := Recover(context.Background(), b.store, reader{
		fmt.Sprintf("attempt-%d", sessions[0]): {report(t, 1, event(sessions[0], "view")),
			report(t, 2, event(sessions[0], "grep")), report(t, 3, event(sessions[0], "edit"))},
		fmt.Sprintf("attempt-%d", sessions[1]): {report(t, 2, event(sessions[1], "grep")), unread},
	})
	if kept != 3 || err == nil || !strings.Contains(err.Error(), fmt.Sprint(sessions[1])) {
		t.Errorf("recovery kept %d reports and answered %v; want the 3 that follow and session %d's refusal",
			kept, err, sessions[1])
	}
	if a, c := b.session(t, requests[0], sessions[0]).Events, b.session(t, requests[1], sessions[1]).Events; a != 3 || c != 2 {
		t.Errorf("the sessions hold %d and %d events; want 3, and 2 up to the report that does not follow", a, c)
	}
}

// drafted is a run of acme/self-hosted-runner over octo/app and octo/lib, each database as a draft
// reads it, and the signed decision a press of Draft records for the run.
func (b bench) drafted(t *testing.T) (run int64, databases map[string]string, decision string) {
	t.Helper()
	databases = map[string]string{}
	var held []store.ScopedDatabase
	for _, name := range []string{"octo/app", "octo/lib"} {
		db := t.TempDir()
		for file, body := range map[string]string{"codeql-database.yml": "sourceLocationPrefix: /src\n",
			"src.zip": "PK-" + name, "db-actions/default/pages": "never read by a draft"} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(db, file)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(db, file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		databases[name] = db
		held = append(held, store.ScopedDatabase{DatabaseIdentity: store.DatabaseIdentity{Repository: name,
			Commit: "c1", Mode: store.AnalysisModeRegular}, Status: store.DatabaseRetained, Path: db})
	}
	run, err := b.store.CreateAnalysisRun(store.AnalysisRequest{RequestedBy: "operator",
		Queries: []store.AnalysisQuery{{QueryID: "acme/self-hosted-runner", QueryVersion: "v1"}}, Databases: held})
	if err != nil {
		t.Fatal(err)
	}
	return run, databases, b.decideDraft(t, run)
}

func (b bench) decideDraft(t *testing.T, run int64) string {
	t.Helper()
	ctx := context.Background()
	point, err := b.store.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := point.Decide(ctx, authz.Request{Operation: authz.OpAuthoring, Principal: authz.Local(""),
		Subject: authz.RouteSubject("POST /api/runs/{id}/drafts"), Allow: true, Reason: "decided",
		Evidence: map[string]string{"path": fmt.Sprintf("/api/runs/%d/drafts", run)}})
	if err != nil {
		t.Fatal(err)
	}
	return d.Digest()
}

var picked = []harness.Picked{
	{Repository: "octo/app", Kind: store.RequestFalseAlarm, FilePath: ".github/workflows/ci.yml", StartLine: 4,
		Message: "runs on a self-hosted runner"},
	{Repository: "octo/lib", Kind: store.RequestMissed},
}

func (b bench) draft(t *testing.T, run int64) store.DraftSession {
	t.Helper()
	drafts, err := b.store.DraftSessions(run)
	if err != nil || len(drafts) == 0 {
		t.Fatalf("run %d's drafts read %+v %v", run, drafts, err)
	}
	return drafts[0]
}

// overDraft waits for the draft on run to end, and answers how.
func (b bench) overDraft(t *testing.T, run int64) store.DraftSession {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d := b.draft(t, run)
		if d.State != store.SessionQueued && d.State != store.SessionRunning {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("the draft on run %d is still %s", run, d.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A draft waits its turn with the sessions on requests, and its worker is handed what the draft tools
// read of each picked repository's database, and nothing else of it; what it drafts comes in its
// reports, and it lands nothing.
func TestADraftWaitsItsTurnAndIsHandedWhatItReads(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	working := b.start(t, request, decision)
	b.workers.next(t)
	run, _, drafting := b.drafted(t)
	asked, err := b.pool.Draft(run, "acme/self-hosted-runner", picked, "operator", drafting)
	if err != nil {
		t.Fatal(err)
	}
	if asked.State != store.SessionQueued || asked.Ahead != 0 {
		t.Errorf("the draft reads %s with %d ahead; want it waiting first in line", asked.State, asked.Ahead)
	}
	b.workers.leave(working, b.worked(t, working, queryText("acme/self-hosted-runner")+"// 1\n"))
	session := b.workers.next(t)
	if session != asked.RowID {
		t.Fatalf("session %d began, not the draft %d", session, asked.RowID)
	}
	b.workers.mu.Lock()
	task, in := b.workers.tasks[1], b.workers.inputs[1]
	b.workers.mu.Unlock()
	if task.Run != run || task.Request != 0 || len(in.Databases) != 2 {
		t.Fatalf("the draft's worker was handed %+v with databases %v", task, slices.Collect(maps.Keys(in.Databases)))
	}
	for name, archive := range in.Databases {
		files := archived(t, archive)
		if want := []string{"codeql-database.yml", "src.zip"}; !slices.Equal(files, want) {
			t.Errorf("%s's database was handed over as %v; want only %v", name, files, want)
		}
	}
	drafted := store.RequestDraft{Description: "report a self-hosted runner", Compliant: "runs-on: ubuntu-latest",
		NonCompliant: "runs-on: self-hosted", Rationale: "the picks ran there"}
	b.workers.leave(session, left{reports: []execution.Reported{report(t, 1, []store.AuthoringEntry{
		{Kind: store.EntryDraft, Draft: &drafted},
		{Kind: store.EntryEnd, End: &store.SessionEnd{State: store.SessionFinished, Detail: "drafted"}}})},
		landing: &execution.SessionLanding{}})
	d := b.overDraft(t, run)
	if d.State != store.SessionFinished || d.Draft == nil || d.Draft.Digest != store.DraftDigest(drafted) {
		t.Errorf("the draft ended %s (%q) with %+v", d.State, d.Detail, d.Draft)
	}
}

// archived names the files an archive holds, in order.
func archived(t *testing.T, archive []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return out
}

// A draft whose run no longer keeps a picked repository's database when its turn comes ends stopped,
// saying so, and no worker is launched for it.
func TestADraftWhoseDatabaseWentWhileItWaitedEndsStopped(t *testing.T) {
	b := newBench(t, 1)
	request, decision := b.asked(t)
	working := b.start(t, request, decision)
	b.workers.next(t)
	run, databases, drafting := b.drafted(t)
	if _, err := b.pool.Draft(run, "acme/self-hosted-runner", picked, "operator", drafting); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(databases["octo/lib"]); err != nil {
		t.Fatal(err)
	}
	b.workers.leave(working, b.worked(t, working, queryText("acme/self-hosted-runner")+"// 1\n"))
	if d := b.overDraft(t, run); d.State != store.SessionStopped || !strings.Contains(d.Detail, "gone") {
		t.Errorf("the draft ended %s (%q)", d.State, d.Detail)
	}
	b.workers.idle(t)
}

// A draft changes no file, so one whose worker says it changed one lands nothing and fails.
func TestADraftWhoseWorkerChangedAFileLandsNothing(t *testing.T) {
	b := newBench(t, 1)
	run, _, drafting := b.drafted(t)
	asked, err := b.pool.Draft(run, "acme/self-hosted-runner", picked, "operator", drafting)
	if err != nil {
		t.Fatal(err)
	}
	b.workers.next(t)
	l := b.worked(t, asked.RowID, queryText("acme/self-hosted-runner")+"// drafted\n")
	l.reports[0] = report(t, 1, []store.AuthoringEntry{
		{Kind: store.EntryEnd, End: &store.SessionEnd{State: store.SessionFinished, Detail: "drafted"}}})
	b.workers.leave(asked.RowID, l)
	if d := b.overDraft(t, run); d.State != store.SessionFailed || !strings.Contains(d.Detail, "changes no file") {
		t.Errorf("the draft ended %s (%q)", d.State, d.Detail)
	}
	if body, _ := os.ReadFile(filepath.Join(b.pack, filepath.FromSlash(runnerQuery))); string(body) != queryText("acme/self-hosted-runner") {
		t.Error("a draft's change to the query landed")
	}
}

// A draft is stopped as a session on a request is: waiting, it ends unstarted; working, its worker is
// asked to stop.
func TestADraftIsStoppedWaitingOrWorking(t *testing.T) {
	b := newBench(t, 1)
	run, _, drafting := b.drafted(t)
	asked, err := b.pool.Draft(run, "acme/self-hosted-runner", picked, "operator", drafting)
	if err != nil {
		t.Fatal(err)
	}
	b.workers.next(t)
	other, _, waitingDecision := b.drafted(t)
	if _, err := b.pool.Draft(other, "acme/self-hosted-runner", picked, "operator", waitingDecision); err != nil {
		t.Fatal(err)
	}
	if !b.pool.StopDraft(other) {
		t.Fatal("the waiting draft could not be stopped")
	}
	if d := b.overDraft(t, other); d.State != store.SessionStopped || d.StartedAt != "" {
		t.Errorf("the draft stopped while waiting reads %+v", d)
	}
	if !b.pool.StopDraft(run) {
		t.Fatal("the working draft could not be stopped")
	}
	b.workers.stopped(t, asked.RowID)
	b.workers.leave(asked.RowID, left{err: errors.New("stopped")})
	if d := b.overDraft(t, run); d.State != store.SessionStopped {
		t.Errorf("the draft stopped while working ended %s (%q)", d.State, d.Detail)
	}
}

// A draft begins only on the decision that asked for it on its own run.
func TestADraftDecidedForAnotherRunDoesNotBegin(t *testing.T) {
	b := newBench(t, 1)
	run, _, _ := b.drafted(t)
	other, _, _ := b.drafted(t)
	if _, err := b.pool.Draft(run, "acme/self-hosted-runner", picked, "operator", b.decideDraft(t, other)); err != nil {
		t.Fatal(err)
	}
	if d := b.overDraft(t, run); d.State != store.SessionFailed || !strings.Contains(d.Detail, "another run") {
		t.Errorf("a draft decided for another run ended %s (%q)", d.State, d.Detail)
	}
	b.workers.idle(t)
}
