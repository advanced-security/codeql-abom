package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Journal is the store as a session a worker runs sees it. It answers what the session reads from a
// snapshot of the store as it stood when the session started, with what the session has kept since
// laid over it, and refuses what the store would refuse, at the same call. What the session keeps
// becomes entries, in the order it kept them, which the coordinator replays into the store through
// the statements a session in the single binary writes with.
type Journal struct {
	// Now stamps what the session keeps, as the store stamps what arrives; the wall clock when nil.
	Now func() time.Time

	mu      sync.Mutex
	snap    store.AuthoringSnapshot
	request *store.QueryRequest
	tests   map[string][]store.QueryTest
	trees   map[string]store.AuthoringAST
	// tree is the row a tree kept here reads as, so the newest reads as newest, as the store's does.
	tree    int64
	started bool
	ended   bool
	pending []store.AuthoringEntry
	// report, where set, sends a coordinator what is pending as one report, a decision as it is made.
	report  func([]store.AuthoringEntry) error
	sending sync.Mutex
}

var _ Records = (*Journal)(nil)

// newJournal answers from snap, which names the session the coordinator started for the worker.
func newJournal(snap store.AuthoringSnapshot) *Journal {
	j := &Journal{snap: snap, tests: map[string][]store.QueryTest{}, trees: map[string]store.AuthoringAST{}}
	if snap.Request != nil {
		r := *snap.Request
		j.request = &r
	}
	for id, tests := range snap.Tests {
		j.tests[id] = slices.Clone(tests)
	}
	for id, tree := range snap.Trees {
		j.trees[id] = tree
		j.tree = max(j.tree, tree.RowID)
	}
	return j
}

// flush reports what the session kept since its last report, keeping it for the next if it cannot be
// sent, all but drop: a decision not reported is not kept, as where the store cannot record it.
func (j *Journal) flush(drop *store.AuthoringEvent) error {
	if j.report == nil {
		return nil
	}
	j.sending.Lock()
	defer j.sending.Unlock()
	j.mu.Lock()
	entries := j.pending
	j.pending = nil
	j.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	err := j.report(entries)
	if err != nil {
		entries = slices.DeleteFunc(entries, func(e store.AuthoringEntry) bool { return drop != nil && e.Event == drop })
		j.mu.Lock()
		j.pending = append(entries, j.pending...)
		j.mu.Unlock()
	}
	return err
}

// Take hands over what the session kept since the last Take, in the order it kept it.
func (j *Journal) Take() []store.AuthoringEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.pending
	j.pending = nil
	return out
}

func (j *Journal) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func (j *Journal) session() int64 { return j.snap.Session.RowID }

func (j *Journal) own(session int64) error {
	if session != j.session() {
		return fmt.Errorf("session %d is not the session this worker runs", session)
	}
	return nil
}

// target is the query the session's request is about, as the session decided it.
func (j *Journal) target() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.about()
}

// about is the query the session's request is about, as the session has decided it so far.
func (j *Journal) about() string {
	if j.request == nil {
		return ""
	}
	return j.request.QueryID
}

func (j *Journal) QueryRequests() ([]store.QueryRequest, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.request == nil {
		return []store.QueryRequest{}, nil
	}
	return []store.QueryRequest{*j.request}, nil
}

func (j *Journal) QueryCatalog(working ...store.WorkingQuery) ([]store.CatalogQuery, error) {
	if len(working) > 0 {
		return nil, errors.New("a worker reads the catalog as it stood when the session started, " +
			"without a working copy laid over it")
	}
	return slices.Clone(j.snap.Catalog), nil
}

func (j *Journal) QueryTestsOf(queryID string) ([]store.QueryTest, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.tests[queryID]), nil
}

func (j *Journal) NewestASTs() (map[string]store.AuthoringAST, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return maps.Clone(j.trees), nil
}

func (j *Journal) AuditSession(session int64, through ...int64) (store.AuditSession, error) {
	if err := j.own(session); err != nil {
		return store.AuditSession{}, err
	}
	if len(through) > 0 {
		return store.AuditSession{}, errors.New("a worker reads its session as it started, not through an event")
	}
	return j.snap.Session, nil
}

func (j *Journal) AnalysisRun(id int64) (store.AnalysisRun, error) {
	if j.snap.Run == nil || j.snap.Run.RowID != id {
		return store.AnalysisRun{}, fmt.Errorf("run %d is not the run this session drafts from", id)
	}
	return *j.snap.Run, nil
}

func (j *Journal) RecordAuthoringEvent(e store.AuthoringEvent) (int64, error) {
	if err := store.CheckAuthoringEvent(e); err != nil {
		return 0, err
	}
	if err := j.own(e.SessionID); err != nil {
		return 0, err
	}
	j.mu.Lock()
	if e.ReceivedAt == "" {
		e.ReceivedAt = j.now().Format(time.RFC3339Nano)
	}
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryEvent, Event: &e})
	j.mu.Unlock()
	// A decision is with the coordinator before the call it decided runs.
	if e.Hook == HookPreToolUse || e.Hook == HookPermission {
		if err := j.flush(&e); err != nil {
			return 0, fmt.Errorf("reporting the decision: %w", err)
		}
	}
	return 0, nil
}

func (j *Journal) RecordAuthoringAST(a store.AuthoringAST) (int64, error) {
	if err := store.CheckAuthoringAST(a); err != nil {
		return 0, err
	}
	if err := j.own(a.SessionID); err != nil {
		return 0, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if a.QueryID != "" && a.QueryID != j.about() {
		return 0, fmt.Errorf("a tree printed for %s, which the session is not about", a.QueryID)
	}
	a.Digest = store.TreeDigest(a.Body)
	kept := a
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryTree, Tree: &kept})
	j.tree++
	if a.Query == "PrintAST" && a.QueryID != "" {
		a.RowID, a.RequestID, a.Size, a.Body = j.tree, j.snap.Session.RequestID, len(a.Body), ""
		j.trees[a.QueryID] = a
	}
	return j.tree, nil
}

func (j *Journal) AddAuthoringSpend(session int64, spent store.AuthoringSpend) error {
	if err := j.own(session); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntrySpend, Spend: &spent})
	return nil
}

func (j *Journal) SetAuthoringSessionModel(session int64, model string) error {
	if err := j.own(session); err != nil {
		return err
	}
	if model == "" {
		return errors.New("a session runs on a model it names")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryModel, Model: model})
	return nil
}

func (j *Journal) DecideTarget(request int64, queryID, rationale string) error {
	if err := store.CheckTargetDecision(queryID, rationale); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.request == nil || j.request.RowID != request:
		return fmt.Errorf("deciding the target of request %d: %w", request, store.ErrNoQueryRequest)
	case j.request.QueryID != "":
		return fmt.Errorf("request %d is already about %s", request, j.request.QueryID)
	}
	j.request.QueryID, j.request.TargetSource, j.request.TargetDetail = queryID, store.TargetAgent, rationale
	j.request.UpdatedAt = j.now().Format(time.RFC3339)
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryTarget,
		Target: &store.TargetDecision{QueryID: queryID, Rationale: rationale}})
	return nil
}

func (j *Journal) RecordQueryTest(t store.QueryTest) error {
	if err := store.CheckQueryTest(t); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if about := j.about(); about == "" || t.QueryID != about {
		return fmt.Errorf("a test of %q, which the session is not about", t.QueryID)
	}
	if t.RanAt == "" {
		t.RanAt = j.now().Format(time.RFC3339)
	}
	// One result per version under each CodeQL, the latest kept, read in the order the store reads them.
	tests := slices.DeleteFunc(j.tests[t.QueryID], func(o store.QueryTest) bool {
		return o.QueryVersion == t.QueryVersion && o.CLIVersion == t.CLIVersion
	})
	tests = append(tests, t)
	slices.SortStableFunc(tests, func(a, b store.QueryTest) int {
		return cmp.Or(cmp.Compare(a.RanAt, b.RanAt), cmp.Compare(a.QueryVersion, b.QueryVersion),
			cmp.Compare(a.CLIVersion, b.CLIVersion))
	})
	j.tests[t.QueryID] = tests
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryTest, Test: &t})
	return nil
}

func (j *Journal) RecordRequestDraft(d store.RequestDraft) (int64, error) {
	if err := store.CheckRequestDraft(d); err != nil {
		return 0, err
	}
	if j.own(d.SessionID) != nil || j.snap.Session.RequestID != 0 {
		return 0, fmt.Errorf("keeping a draft: session %d is not drafting from a run", d.SessionID)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if d.DraftedAt == "" {
		d.DraftedAt = j.now().Format(time.RFC3339)
	}
	d.Digest = store.DraftDigest(d)
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryDraft, Draft: &d})
	return 0, nil
}

// StartAuthoringSession hands back the session the coordinator started, once.
func (j *Journal) StartAuthoringSession(request int64, _, _ string) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.request == nil || j.request.RowID != request:
		return 0, fmt.Errorf("starting a session on request %d: %w", request, store.ErrNoQueryRequest)
	case j.started:
		return 0, fmt.Errorf("request %d: %w", request, store.ErrSessionUnderWay)
	}
	j.started = true
	return j.session(), nil
}

// StartDraftSession hands back the drafting session the coordinator started, once.
func (j *Journal) StartDraftSession(run int64, _, _, _ string) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.snap.Session.RequestID != 0 || j.snap.Session.RunID != run:
		return 0, fmt.Errorf("drafting from run %d: there is no such run", run)
	case j.started:
		return 0, fmt.Errorf("drafting from run %d: %w", run, store.ErrSessionUnderWay)
	}
	j.started = true
	return j.session(), nil
}

// EndAuthoringSession keeps how the session ended as the worker saw it; the coordinator ends it.
func (j *Journal) EndAuthoringSession(session int64, state, detail string) error {
	if err := store.CheckSessionEnd(state); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.own(session) != nil || j.ended {
		return fmt.Errorf("ending session %d: %w", session, store.ErrSessionNotUnderWay)
	}
	j.ended = true
	j.pending = append(j.pending, store.AuthoringEntry{Kind: store.EntryEnd,
		End: &store.SessionEnd{State: state, Detail: detail}})
	return nil
}

func (j *Journal) AuthoringSessions(request int64) ([]store.AuthoringSession, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.request == nil || j.request.RowID != request {
		return []store.AuthoringSession{}, nil
	}
	return slices.Clone(j.snap.Sessions), nil
}

func (j *Journal) DraftSessions(run int64) ([]store.DraftSession, error) {
	if j.snap.Session.RequestID != 0 || j.snap.Session.RunID != run {
		return []store.DraftSession{}, nil
	}
	return slices.Clone(j.snap.Drafts), nil
}

// Report sends a coordinator what a session kept since its last report, and the calls it has under
// way, answering once the report is written.
type Report func(entries []store.AuthoringEntry, inflight []Flight) error

// Reported is what one report of a worker's session holds, as it travels: what the session kept since
// the report before, and the calls it had under way.
type Reported struct {
	Entries  []store.AuthoringEntry `json:"entries"`
	InFlight []Flight               `json:"inFlight"`
}

// reportEvery is how often a worker's session reports what it kept between the decisions it reports
// as it makes them.
const reportEvery = 2 * time.Second

// Granted runs a session a coordinator started, as a worker runs it: reading snap where the single
// binary reads the store, and reporting what it keeps where the single binary writes it, each decision
// before the call it decided runs. A ctx that ends stops the session, which reports that it did. Once
// the session ends it answers the test directory it was bound to, a slash path relative to the pack.
func (s *Sessions) Granted(ctx context.Context, snap store.AuthoringSnapshot, by string, report Report) (string, error) {
	request, run := snap.Session.RequestID, snap.Session.RunID
	j := newJournal(snap)
	j.report = func(entries []store.AuthoringEntry) error { return report(entries, s.InFlight(request)) }
	s.Store = j
	var done <-chan struct{}
	var err error
	stop := func() { s.Stop(request) }
	switch {
	case request != 0:
		_, done, err = s.start(request, by)
	case run != 0:
		picked := make([]Picked, 0, len(snap.Picks))
		for _, p := range snap.Picks {
			picked = append(picked, Picked(p))
		}
		_, done, err = s.draft(run, snap.Session.QueryID, picked, by)
		stop = func() { s.StopDraft(run) }
	default:
		err = errors.New("the session is on neither a request nor a run")
	}
	if err != nil {
		return "", err
	}
	every := s.ReportEvery
	if every <= 0 {
		every = reportEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for working := true; working; {
		select {
		case <-done:
			working = false
		case <-ctx.Done():
			stop()
			<-done
			working = false
		case <-tick.C:
			if err := j.flush(nil); err != nil {
				slog.Warn("authoring: a report could not be written; what it held goes with the next", "error", err)
			}
		}
	}
	if err := j.flush(nil); err != nil {
		return "", fmt.Errorf("reporting how the session ended: %w", err)
	}
	tests := ""
	if target := j.target(); target != "" {
		if _, dir, err := boundTo(s.Workspace, target); err == nil && dir != "" {
			rel, _ := filepath.Rel(s.Workspace, dir)
			tests = filepath.ToSlash(rel)
		}
	}
	return tests, nil
}
