package harness

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ErrBusy is a session asked for while CodeQL is in use on this machine.
var ErrBusy = errors.New("CodeQL is in use by a crawl, a run or a test on this machine, and a " +
	"session holds it for as long as it works; start the session when that has finished")

// Sessions starts authoring sessions, one per request, and keeps each one's timeline.
type Sessions struct {
	Store *store.Store
	// Workspace is the operator's pack, the only place a session writes.
	Workspace string
	// Libraries are read-only roots a query is written against: the CodeQL package cache.
	Libraries []string
	// WorkDir holds each session's home while it runs.
	WorkDir string
	// Agent runs a session; nil refuses every start with the reason. Change it while serving
	// through SetAgent.
	Agent Agent
	// Model is recorded on each session.
	Model string
	// Lock is held for a session's lifetime: ql-mcp's CodeQL runs take none of ABOM's.
	Lock *runner.Lock
	// Limit is how long one session may run; zero takes DefaultLimit.
	Limit time.Duration
	// Wait is how long a start waits for CodeQL to be free before refusing.
	Wait time.Duration
	// Version is the CodeQL ql-mcp and ABOM run, which every tree and test result names.
	Version func(context.Context) (string, error)
	// RunTests runs a query's own tests for a session, which already holds Lock.
	RunTests authoring.Tests
	// Prepare installs what the pack depends on wherever it has no lock, before the agent has
	// control: ABOM's act and never the session's, which cannot install packs. Called holding Lock;
	// nil installs nothing.
	Prepare func(context.Context) error

	agentMu sync.RWMutex
	mu      sync.Mutex
	live    map[int64]*live
	// drafting is the sessions drafting from a run, by run.
	drafting map[int64]*live
}

// SetAgent changes what runs the next session; one already under way keeps its own.
func (s *Sessions) SetAgent(a Agent) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	s.Agent = a
}

func (s *Sessions) agent() Agent {
	s.agentMu.RLock()
	defer s.agentMu.RUnlock()
	return s.Agent
}

// DefaultLimit bounds a session nobody configured a limit for.
const DefaultLimit = 45 * time.Minute

type live struct {
	session int64
	gate    *Gate
	cancel  context.CancelFunc
	stopped bool
}

type startedAuthoring struct {
	id      int64
	request int64
	dir     string
	limit   time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
	live    *live
	setup   Setup
	unlock  func()
}

// Start begins a session on a request and answers once it is under way; the agent works on in
// the background, and its timeline is the store's.
func (s *Sessions) Start(request int64, by string) (store.AuthoringSession, error) {
	session, _, err := s.start(request, by)
	return session, err
}

func (s *Sessions) start(request int64, by string) (store.AuthoringSession, <-chan struct{}, error) {
	if s.agent() == nil {
		return store.AuthoringSession{}, nil, ErrNoAgent
	}
	req, err := s.requestNamed(request)
	if err != nil {
		return store.AuthoringSession{}, nil, err
	}
	if req.Status == store.RequestDone || req.Status == store.RequestDeclined {
		return store.AuthoringSession{}, nil, fmt.Errorf("request %d is %s", request, req.Status)
	}
	if _, ok := s.running(request); ok {
		return store.AuthoringSession{}, nil, fmt.Errorf("request %d: %w", request, store.ErrSessionUnderWay)
	}
	bounds, err := s.bounds(req)
	if err != nil {
		return store.AuthoringSession{}, nil, err
	}
	unlock, err := s.acquire()
	if err != nil {
		return store.AuthoringSession{}, nil, err
	}
	id, err := s.Store.StartAuthoringSession(request, by, s.Model)
	if err != nil {
		unlock()
		return store.AuthoringSession{}, nil, err
	}
	dir, home, scratch, bounds, detail, err := s.prepareAuthoringBounds(id, bounds)
	if err != nil {
		return s.failStarted(id, unlock, detail, err)
	}
	guard := s.firstChange(request)
	bounds = bounds.Guard(guard)

	gate := NewGate(bounds, s.recorder(id))
	if err := gate.KeepPrinted(scratch, s.keeper(id, request)); err != nil {
		return s.failStarted(id, unlock, err.Error(), err)
	}
	gate.KeepSpend(func(sp Spend) error {
		return s.Store.AddAuthoringSpend(id, store.AuthoringSpend{InputTokens: sp.InputTokens,
			OutputTokens: sp.OutputTokens, NanoAIU: sp.NanoAIU})
	})
	limit := s.limit()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	l := &live{session: id, gate: gate, cancel: cancel}
	s.recordLive(request, l)

	setup := Setup{
		Home:      home,
		Scratch:   scratch,
		Workspace: bounds.Workspace(),
		Prompt:    prompt(req),
		Rules:     rules(bounds, s.Libraries, req.QueryID),
		Gate:      gate,
		Tools:     s.tools(ctx, request, gate),
		Next:      s.next(request, gate, guard),
	}
	done := s.launchAuthoring(startedAuthoring{
		id: id, request: request, dir: dir, limit: limit, ctx: ctx, cancel: cancel, live: l,
		setup: setup, unlock: unlock,
	})
	session, err := s.session(request, id)
	return session, done, err
}

func (s *Sessions) prepareAuthoringBounds(
	id int64, bounds Bounds,
) (dir, home, scratch string, out Bounds, detail string, err error) {
	dir = filepath.Join(s.WorkDir, fmt.Sprintf("session-%d", id))
	home, scratch = filepath.Join(dir, "copilot"), filepath.Join(dir, "ql-mcp")
	for _, d := range []string{home, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return dir, home, scratch, bounds, "preparing the session's directory: " + err.Error(), err
		}
	}
	if bounds, err = bounds.Deny(home); err == nil {
		bounds, err = bounds.Read(scratch)
	}
	if err != nil {
		return dir, home, scratch, bounds, "bounding the session: " + err.Error(), err
	}
	return dir, home, scratch, bounds, "", nil
}

func (s *Sessions) failStarted(
	id int64, unlock func(), detail string, err error,
) (store.AuthoringSession, <-chan struct{}, error) {
	unlock()
	_ = s.Store.EndAuthoringSession(id, store.SessionFailed, detail)
	return store.AuthoringSession{}, nil, err
}

func (s *Sessions) limit() time.Duration {
	limit := s.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	return limit
}

func (s *Sessions) recordLive(request int64, l *live) {
	s.mu.Lock()
	if s.live == nil {
		s.live = map[int64]*live{}
	}
	s.live[request] = l
	s.mu.Unlock()
}

func (s *Sessions) launchAuthoring(run startedAuthoring) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer run.unlock()
		final, err := "", s.prepare(run.ctx)
		if err == nil {
			final, err = s.run(run.ctx, run.setup)
		}
		state, detail := s.outcome(run.ctx, run.live, run.limit, final, err)
		run.cancel()
		s.mu.Lock()
		delete(s.live, run.request)
		s.mu.Unlock()
		_ = os.RemoveAll(run.dir)
		_ = s.Store.EndAuthoringSession(run.id, state, detail)
	}()
	return done
}

// prepare installs the pack a session works in. A pack that cannot be installed compiles nothing
// the agent writes, so the session ends there, saying why.
func (s *Sessions) prepare(ctx context.Context) error {
	if s.Prepare == nil {
		return nil
	}
	if err := s.Prepare(ctx); err != nil {
		return fmt.Errorf("installing what the query pack depends on, before the agent started: %w", err)
	}
	return nil
}

// run keeps a panicking agent from taking the server with it.
func (s *Sessions) run(ctx context.Context, setup Setup) (final string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the session failed: %v", r)
		}
	}()
	agent := s.agent()
	if agent == nil {
		return "", ErrNoAgent
	}
	return agent.Run(ctx, setup)
}

func (s *Sessions) outcome(ctx context.Context, l *live, limit time.Duration, final string, err error) (string, string) {
	s.mu.Lock()
	stopped := l.stopped
	s.mu.Unlock()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return store.SessionStopped, fmt.Sprintf("stopped at the session's time limit of %s", limit)
	case stopped:
		return store.SessionStopped, "stopped by an operator"
	case err != nil:
		return store.SessionFailed, clip(err.Error())
	case strings.TrimSpace(final) == "":
		return store.SessionFinished, "the agent ended without a last word"
	}
	return store.SessionFinished, clip(final)
}

// Stop ends the session working a request, and reports whether there was one on this server.
func (s *Sessions) Stop(request int64) bool {
	s.mu.Lock()
	l, ok := s.live[request]
	if ok {
		l.stopped = true
	}
	s.mu.Unlock()
	if ok {
		l.cancel()
	}
	return ok
}

// InFlight is the ql-mcp calls under way in a request's session, nil when none is working it.
func (s *Sessions) InFlight(request int64) []Flight {
	l, ok := s.running(request)
	if !ok {
		return nil
	}
	return l.gate.InFlight()
}

func (s *Sessions) running(request int64) (*live, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.live[request]
	return l, ok
}

func (s *Sessions) acquire() (func(), error) {
	wait := s.Wait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	unlock, err := s.Lock.Acquire(ctx)
	if err != nil {
		return nil, ErrBusy
	}
	return unlock, nil
}

func (s *Sessions) recorder(session int64) func(Event) error {
	return func(e Event) error {
		var took *int64
		if e.Took > 0 {
			ms := e.Took.Milliseconds()
			took = &ms
		}
		_, err := s.Store.RecordAuthoringEvent(store.AuthoringEvent{
			SessionID: session, At: e.At.UTC().Format(time.RFC3339Nano), Hook: e.Hook, Tool: e.Tool,
			Subject: e.Subject, Decision: e.Decision, Step: e.Step, Reason: e.Reason, Detail: e.Detail,
			TookMs: took,
		})
		return err
	}
}

// keeper keeps a printout against the request's query as it stands, the fixtures it was printed
// from and the CodeQL that printed it, so a tree printed before either changed reads as old.
func (s *Sessions) keeper(session, request int64) func(Printed) (int64, error) {
	return func(p Printed) (int64, error) {
		req, err := s.requestNamed(request)
		if err != nil {
			return 0, err
		}
		var digest string
		if req.QueryID != "" {
			if q, found, err := authoring.Find(s.Workspace, req.QueryID); err == nil && found {
				digest, _ = authoring.DigestFixtures(s.Workspace, q.Tests)
			}
		}
		cli, err := s.codeQL(context.Background())
		if err != nil {
			return 0, err
		}
		return s.Store.RecordAuthoringAST(store.AuthoringAST{
			SessionID: session, QueryID: req.QueryID, PrintedAt: p.At.UTC().Format(time.RFC3339Nano),
			Query: p.Query, Database: p.Database, SourceFiles: p.SourceFiles, TestDigest: digest,
			Body: p.Body, Truncated: p.Truncated, CLIVersion: cli,
		})
	}
}

// codeQL is the CodeQL the session's CLI calls run under.
func (s *Sessions) codeQL(ctx context.Context) (string, error) {
	if s.Version == nil {
		return "", errors.New("this server cannot say which CodeQL it runs, so nothing it printed or " +
			"tested can be kept")
	}
	return s.Version(ctx)
}

// firstChange keeps the target query as it is until everything it is judged by is on record: its
// help, its .expected, its tests failing on a result the fixtures mark, and a tree printed from
// the fixtures as they are.
//
// Once the query has changed there is nothing left to hold: the first change could only get past
// this with all of them on record.
func (s *Sessions) firstChange(request int64) func() string {
	var mu sync.Mutex
	var start string
	released := false
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return ""
		}
		req, err := s.requestNamed(request)
		if err != nil {
			return err.Error()
		}
		q, found, err := authoring.Find(s.Workspace, req.QueryID)
		switch {
		case err != nil:
			return err.Error()
		case !found:
			return req.QueryID + " is not in the pack, so ABOM cannot tell whether its tests failed first"
		}
		if start == "" {
			start = q.Version
		}
		if q.Version != start {
			released = true
			return ""
		}
		why, err := s.unjudged(q)
		if err != nil {
			return err.Error()
		}
		released = why == ""
		return why
	}
}

// unjudged names the first link missing from what a query is judged by, empty when none is.
func (s *Sessions) unjudged(q authoring.Query) (string, error) {
	steps, err := s.progress(context.Background(), q)
	if err != nil {
		return "", err
	}
	if st := steps[StepHelp]; st.State != authoring.Passing {
		return "the query stays as it is until its help says what it reports: " + st.Detail +
			"; write the help from the request first", nil
	}
	if st := steps[StepExpected]; st.State != authoring.Passing {
		return "the query stays as it is until its .expected says what it must report: " + st.Detail +
			"; write it from the help and the fixtures' # $ Alert marks", nil
	}
	fixtures, err := authoring.DigestFixtures(s.Workspace, q.Tests)
	if err != nil {
		return "", err
	}
	tests, err := s.Store.QueryTestsOf(q.ID)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(tests, func(t store.QueryTest) bool {
		return fixtures != "" && t.QueryVersion == q.Version && t.FixturesDigest == fixtures &&
			authoring.FailedOnMarks(t)
	}) {
		return fmt.Sprintf("the query stays as it is until its tests have failed on a result its fixtures "+
			"mark: mark each line the query must report with # $ Alert, then call %s, which runs them "+
			"against the query as it stands and records the failure", ToolTest), nil
	}
	if st := steps[StepAST]; st.State != authoring.Passing {
		return "the query stays as it is until PrintAST has printed these fixtures: " + st.Detail +
			"; run codeql_query_run with queryName PrintAST, queryLanguage actions, the test database " +
			"and sourceFiles naming the fixtures", nil
	}
	return "", nil
}

// progress is a query's steps by name, read from the files and the evidence on record now.
func (s *Sessions) progress(ctx context.Context, q authoring.Query) (map[string]authoring.Step, error) {
	cli, err := s.codeQL(ctx)
	if err != nil {
		return nil, err
	}
	tests, err := s.Store.QueryTestsOf(q.ID)
	if err != nil {
		return nil, err
	}
	printed, err := s.Store.NewestASTs()
	if err != nil {
		return nil, err
	}
	steps, err := authoring.Progress(s.Workspace, q,
		authoring.Evidence{CLI: cli, Tests: tests, Printed: printed[q.ID]})
	if err != nil {
		return nil, err
	}
	out := make(map[string]authoring.Step, len(steps))
	for _, st := range steps {
		out[st.Step] = st
	}
	return out, nil
}

// next is what an idle agent is told: the first thing not done, recorded on the timeline. It is
// empty when nothing is left, or when the agent would be told the same about the same files again.
func (s *Sessions) next(request int64, gate *Gate, guard func() string) func(context.Context) string {
	var last string
	return func(ctx context.Context) string {
		nudge, files := s.nudge(ctx, request, gate, guard)
		if nudge == "" || nudge+files == last {
			return ""
		}
		last = nudge + files
		gate.Say(nudge)
		return nudge
	}
}

// nudge is the first thing not done for a request, and the query and tests it was read from.
func (s *Sessions) nudge(ctx context.Context, request int64, gate *Gate, guard func() string) (string, string) {
	if !gate.Bounds().bound() {
		return fmt.Sprintf("Not finished: no query is chosen. Call %s, then %s.", ToolCatalog, ToolTarget), ""
	}
	req, err := s.requestNamed(request)
	if err != nil {
		return "", ""
	}
	q, found, err := authoring.Find(s.Workspace, req.QueryID)
	if err != nil || !found {
		return "", ""
	}
	digest, err := authoring.DigestTests(s.Workspace, q.Tests)
	if err != nil {
		return "", ""
	}
	files := q.Version + " " + digest
	if why := guard(); why != "" {
		return "Not finished: " + why + ".", files
	}
	steps, err := s.progress(ctx, q)
	if err != nil {
		return "", ""
	}
	for _, st := range []struct{ step, do string }{
		{StepHelp, "Write the help from the request"},
		{StepExpected, "Write the .expected from the help and the fixtures' marks"},
		{StepAST, "Print the fixtures with PrintAST"},
		{StepCompiles, "Write the query so it compiles; check it with codeql_query_compile, then call " + ToolTest},
		{StepTests, "Write the query so its tests pass: read the .actual beside the test, change the " +
			"query, compile it, and call " + ToolTest + " again"},
	} {
		if got := steps[st.step]; got.State != authoring.Passing {
			return fmt.Sprintf("Not finished: %s is %s: %s. %s.", st.step, got.State, got.Detail, st.do), files
		}
	}
	return "", files
}

func (s *Sessions) requestNamed(id int64) (store.QueryRequest, error) {
	all, err := s.Store.QueryRequests()
	if err != nil {
		return store.QueryRequest{}, err
	}
	for _, r := range all {
		if r.RowID == id {
			return r, nil
		}
	}
	return store.QueryRequest{}, fmt.Errorf("request %d: %w", id, store.ErrNoQueryRequest)
}

func (s *Sessions) session(request, id int64) (store.AuthoringSession, error) {
	all, err := s.Store.AuthoringSessions(request)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	for _, a := range all {
		if a.RowID == id {
			return a, nil
		}
	}
	return store.AuthoringSession{}, fmt.Errorf("session %d is not on record", id)
}

// bounds are the pack and libraries to read, and the operator's target to write when they chose one.
func (s *Sessions) bounds(req store.QueryRequest) (Bounds, error) {
	b, err := NewBounds(s.Workspace, s.Libraries)
	if err != nil {
		return Bounds{}, err
	}
	// Everything under the pack is readable, so a session directory there would expose the CLI's state.
	if s.WorkDir == "" {
		return Bounds{}, errors.New("no directory is configured for session state")
	}
	work, err := b.resolve(s.WorkDir)
	if err != nil {
		return Bounds{}, err
	}
	if within(b.Workspace(), work) {
		return Bounds{}, fmt.Errorf("the session directory %s must not sit inside the pack at %s",
			s.WorkDir, s.Workspace)
	}
	if req.QueryID == "" {
		return b, nil
	}
	if err := s.layOutAsked(req); err != nil {
		return Bounds{}, err
	}
	query, tests, err := s.locate(req.QueryID)
	if err != nil {
		return Bounds{}, err
	}
	return b.Bind(query, tests)
}

// layOutAsked lays out the new query an operator asked for, the first time a session works it.
func (s *Sessions) layOutAsked(req store.QueryRequest) error {
	if req.TargetSource != store.TargetOperator || req.TargetDecision != store.DecisionCreate {
		return nil
	}
	if _, found, err := authoring.Find(s.Workspace, req.QueryID); err != nil || found {
		return err
	}
	name := queryFileName(req.QueryID)
	if !customql.ValidQueryName(name) {
		return fmt.Errorf("%s cannot be laid out: %q, from the last part of its id, is not a file name "+
			"such as CheckoutTagPin", req.QueryID, name)
	}
	if taken, err := s.taken(req.QueryID, name); err != nil || taken != "" {
		if err != nil {
			return err
		}
		return fmt.Errorf("%s cannot be laid out: %s", req.QueryID, taken)
	}
	_, _, err := customql.ScaffoldQuery(s.Workspace, name, req.QueryID, req.RowID, requested(req))
	return err
}

// queryFileName is the file a new query is laid out in, from the last part of its id:
// acme/checkout-tag-pin is CheckoutTagPin.
func queryFileName(id string) string {
	var out strings.Builder
	for word := range strings.SplitSeq(id[strings.LastIndex(id, "/")+1:], "-") {
		if word != "" {
			out.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return out.String()
}

// requested is what a request asks its new query to declare, with the tags its references imply.
func requested(r store.QueryRequest) customql.Requested {
	tags := slices.Clone(r.Metadata.Tags)
	for _, t := range r.ReferenceTags {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	refs := []string{}
	for _, ref := range r.References {
		refs = append(refs, ref.Value)
	}
	return customql.Requested{Title: r.Title, Description: r.Metadata.Description,
		Precision: r.Metadata.Precision, ProblemSeverity: r.Metadata.ProblemSeverity,
		SecuritySeverity: r.Metadata.SecuritySeverity, Tags: tags, Recommendation: r.Recommendation,
		References: refs}
}

// locate finds a query in the pack by id, and the one test directory that is its alone, laying
// one out if it has none.
func (s *Sessions) locate(id string) (string, string, error) {
	queries, err := authoring.Workspace(s.Workspace)
	if err != nil {
		return "", "", err
	}
	var found []authoring.Query
	for _, q := range queries {
		if q.ID == id {
			found = append(found, q)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("%s is not a query in the pack at %s, and a session writes nowhere else",
			id, s.Workspace)
	case 1:
	default:
		return "", "", fmt.Errorf("%s is declared by both %s and %s", id, found[0].Rel, found[1].Rel)
	}
	q := found[0]
	dirs := []string{}
	for _, t := range q.Tests {
		if dir := filepath.Dir(t); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	name := strings.TrimSuffix(filepath.Base(q.Path), ".ql")
	switch len(dirs) {
	case 0:
		if filepath.Dir(filepath.FromSlash(q.Rel)) != "src" || !customql.ValidQueryName(name) {
			return "", "", fmt.Errorf("%s has no test, and one is laid out only for a query directly "+
				"under src/ with a plain name", q.Rel)
		}
		tests, err := customql.ScaffoldTest(s.Workspace, name)
		return q.Path, tests, err
	case 1:
	default:
		return "", "", fmt.Errorf("%s is tested in %d directories, and a session writes one", q.Rel, len(dirs))
	}
	for _, other := range queries {
		for _, t := range other.Tests {
			if other.ID != id && filepath.Dir(t) == dirs[0] {
				return "", "", fmt.Errorf("%s also holds the test of %s, whose fixtures a session "+
					"must not change", dirs[0], other.ID)
			}
		}
	}
	return q.Path, dirs[0], nil
}

// targetChoice is the agent's decision of which query a request is about.
type targetChoice struct {
	Decision  string `json:"decision" jsonschema:"improve to change a query already in the pack, or create to add a new one"`
	QueryID   string `json:"queryId" jsonschema:"the query's @id; for create, a new id such as acme/checkout-tag-pin"`
	Name      string `json:"name,omitempty" jsonschema:"for create, the new query's file name without .ql, such as CheckoutTagPin"`
	Rationale string `json:"rationale" jsonschema:"why this query and not another in the catalog, in a sentence or two"`
}

func (s *Sessions) tools(ctx context.Context, request int64, gate *Gate) []copilot.Tool {
	catalog := copilot.DefineTool(ToolCatalog,
		"Lists the queries ABOM knows: those in your pack, which a session can improve, and those "+
			"it has run from other packs, which it cannot change.",
		answering(func(struct{}) (string, error) { return s.catalog() }))
	target := copilot.DefineTool(ToolTarget,
		"Decides the query this request is about, once: improve one in your pack, or create a new "+
			"one. ABOM records the decision and lays out the query's files, and the session may then "+
			"write that query and nothing else.",
		answering(func(c targetChoice) (string, error) { return s.decide(request, gate, c) }))
	test := copilot.DefineTool(ToolTest,
		"Runs the target query's own tests through ABOM, which records what they said. Call it once "+
			"the help, the fixtures marking each line the query must report with # $ Alert, and the "+
			".expected are written, before changing the query: its first change waits until these "+
			"tests have failed because those results are missing. It also extracts the test database.",
		answering(func(struct{}) (string, error) { return s.testTarget(ctx, request, gate) }))
	catalog.SkipPermission, target.SkipPermission, test.SkipPermission = true, true, true
	return []copilot.Tool{catalog, target, test}
}

// testTarget runs the bound query's own tests through ABOM's runner and records them, saying
// whether the failure is one on a marked result.
func (s *Sessions) testTarget(ctx context.Context, request int64, gate *Gate) (string, error) {
	if !gate.Bounds().bound() {
		return "", fmt.Errorf("decide the query this request is about with %s before testing it", ToolTarget)
	}
	if s.RunTests == nil {
		return "", errors.New("ABOM cannot run tests for this session; use codeql_test_run")
	}
	req, err := s.requestNamed(request)
	if err != nil {
		return "", err
	}
	q, found, err := authoring.Find(s.Workspace, req.QueryID)
	switch {
	case err != nil:
		return "", err
	case !found:
		return "", fmt.Errorf("%s is not in the pack", req.QueryID)
	}
	cli, err := s.codeQL(ctx)
	if err != nil {
		return "", err
	}
	result, err := authoring.RecordTests(ctx, s.Store, s.RunTests, s.Workspace, q, cli)
	if err != nil {
		return "", err
	}
	switch {
	case result.Passed:
		return result.Detail + ". They pass, and ABOM recorded it.", nil
	case authoring.FailedOnMarks(store.QueryTest{FailureStage: result.FailureStage,
		Missing: result.Missing, Unexpected: result.Unexpected}):
		return result.Detail + ". ABOM recorded this failure: the fixtures mark results this version " +
			"does not meet, and the .actual beside the test lists them under testFailures.", nil
	}
	return result.Detail + ". This is not a failure on a marked result, so it does not show the tests " +
		"can catch a query that misses one: mark each line the query must report with # $ Alert.", nil
}

// answering hands a tool's refusal to the agent as the tool's answer: the SDK shows the model a
// handler's error only as "Tool execution failed".
func answering[T any](handle func(T) (string, error)) func(T, copilot.ToolInvocation) (copilot.ToolResult, error) {
	return func(args T, _ copilot.ToolInvocation) (copilot.ToolResult, error) {
		text, err := handle(args)
		answer := copilot.ToolResult{TextResultForLLM: text, ResultType: "success"}
		if err != nil {
			answer = copilot.ToolResult{TextResultForLLM: err.Error(), ResultType: "failure", Error: err.Error()}
		}
		return answer, nil
	}
}

func (s *Sessions) catalog() (string, error) {
	queries, err := authoring.Workspace(s.Workspace)
	if err != nil {
		return "", err
	}
	known, err := s.Store.QueryCatalog()
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Queries in your pack at %s, which a session can improve:\n", s.Workspace)
	inPack := map[string]bool{}
	for _, q := range queries {
		inPack[q.ID] = true
		tested := "no test yet"
		if len(q.Tests) > 0 {
			rel, _ := filepath.Rel(s.Workspace, filepath.Dir(q.Tests[0]))
			tested = "tested in " + filepath.ToSlash(rel)
		}
		fmt.Fprintf(&out, "- %s (%s): %s, %s\n", q.ID, q.Kind, q.Rel, tested)
	}
	if len(queries) == 0 {
		out.WriteString("- none yet\n")
	}
	out.WriteString("Queries ABOM has run from other packs, which a session cannot change; their " +
		"sources are readable under the library roots:\n")
	listed := 0
	for _, q := range known {
		if q.Lane || q.Discovery || inPack[q.QueryID] {
			continue
		}
		fmt.Fprintf(&out, "- %s (%s) from %s\n", q.QueryID, q.Kind, q.Pack)
		listed++
	}
	if listed == 0 {
		out.WriteString("- none on record\n")
	}
	return out.String(), nil
}

func (s *Sessions) decide(request int64, gate *Gate, c targetChoice) (string, error) {
	if b := gate.Bounds(); b.bound() {
		return "", fmt.Errorf("this session is already about %s; work on it", b.rel(b.query))
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return "", errors.New("say why this query, in the rationale")
	}
	var query, tests string
	switch c.Decision {
	case "improve":
		var err error
		if query, tests, err = s.locate(c.QueryID); err != nil {
			return "", err
		}
		if err := s.Store.DecideTarget(request, c.QueryID, c.Rationale); err != nil {
			return "", err
		}
	case "create":
		if !customql.ValidQueryID(c.QueryID) {
			return "", fmt.Errorf("%q is not a query id such as acme/checkout-tag-pin", c.QueryID)
		}
		if !customql.ValidQueryName(c.Name) {
			return "", fmt.Errorf("%q is not a plain file name such as CheckoutTagPin", c.Name)
		}
		if taken, err := s.taken(c.QueryID, c.Name); err != nil || taken != "" {
			if err != nil {
				return "", err
			}
			return "", errors.New(taken)
		}
		req, err := s.requestNamed(request)
		if err != nil {
			return "", err
		}
		if err := s.Store.DecideTarget(request, c.QueryID, c.Rationale); err != nil {
			return "", err
		}
		if query, tests, err = customql.ScaffoldQuery(s.Workspace, c.Name, c.QueryID, request,
			requested(req)); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("the decision is improve or create, not %q", c.Decision)
	}
	if err := gate.Bind(query, tests); err != nil {
		return "", err
	}
	b := gate.Bounds()
	return fmt.Sprintf("This session is now about %s, in %s, which is on disk: change it with edit, "+
		"since create refuses a file that exists. Write fixtures under %s, marking each line to report "+
		"with # $ Alert, then call %s before changing the query.", c.QueryID, b.query,
		filepath.Join(b.tests, ".github", "workflows"), ToolTest), nil
}

// taken says why a new query cannot have this id or file name, or is empty when it can.
func (s *Sessions) taken(id, name string) (string, error) {
	queries, err := authoring.Workspace(s.Workspace)
	if err != nil {
		return "", err
	}
	for _, q := range queries {
		switch {
		case q.ID == id:
			return fmt.Sprintf("%s is already %s in your pack; improve it instead", id, q.Rel), nil
		case strings.EqualFold(strings.TrimSuffix(filepath.Base(q.Path), ".ql"), name):
			return fmt.Sprintf("%s already holds %s; choose another file name", q.Rel, q.ID), nil
		}
	}
	if _, err := os.Stat(filepath.Join(s.Workspace, "src", name+".ql")); err == nil {
		return fmt.Sprintf("src/%s.ql is already there; choose another file name", name), nil
	}
	// An empty directory claims nothing; one holding a file holds somebody's fixtures.
	held := false
	_ = filepath.WalkDir(filepath.Join(s.Workspace, "test", name), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			held = true
			return filepath.SkipAll
		}
		return nil
	})
	if held {
		return fmt.Sprintf("test/%s already holds files; choose another file name", name), nil
	}
	known, err := s.Store.QueryCatalog()
	if err != nil {
		return "", err
	}
	for _, q := range known {
		if q.QueryID == id {
			return fmt.Sprintf("%s is already a query ABOM has run, from %s", id, q.Pack), nil
		}
	}
	return "", nil
}

// rules are appended to the system message: what the session is about, what it may touch, and
// the order the loop goes in.
func rules(b Bounds, libraries []string, target string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "You are working one request for a CodeQL query over GitHub Actions workflows, "+
		"inside ABOM, in the query pack at %s. ABOM decides and records every tool call before it "+
		"runs. A refused call says why, and the same call again is refused again.\n\n", b.Workspace())
	fmt.Fprintf(&out, "You have no shell. Read the pack and the CodeQL library sources under %s with "+
		"view, glob and grep; write with create and edit; use ql-mcp's %s; and ABOM's %s, %s and %s.\n\n",
		strings.Join(libraries, ", "), strings.Join(loopNames(), ", "), ToolCatalog, ToolTarget, ToolTest)
	if b.bound() {
		fmt.Fprintf(&out, "This session is about %s, the query in %s, tested in %s.\n\n", target, b.query,
			b.tests)
	} else {
		fmt.Fprintf(&out, "No query has been chosen for this request. Call %s, then %s: improve a "+
			"query already in your pack, or create a new one with a new id and file name. Nothing can "+
			"be written until you have decided.\n\n", ToolCatalog, ToolTarget)
	}
	out.WriteString("You may write the target query, its help in a .md file beside it, its .expected, and " +
		"Actions YAML fixtures under its test directory: workflows under .github/workflows there, or " +
		"action metadata. create writes a new file and refuses one that exists; change a file with edit. " +
		"The .qlref, the .actual, the test database and every manifest belong to ABOM and the CLI.\n\n")
	out.WriteString("Work test-first, in this order. The query cannot change until 1 to 5 are done.\n" +
		"1. Write the help in these sections and no others: a # title, ## Overview (what the query " +
		"reports and why it matters), ## Recommendation (how to fix it), ## Example (the request's " +
		"examples, non-compliant then compliant) and ## References. Where it was laid out from the " +
		"request, replace every " + customql.HelpUnwritten + " with what that section should say.\n" +
		"2. Write one fixture per example of the request, each its own workflow file. Then give " +
		"every case and edge case the query detects at least one non-compliant fixture and at least " +
		"one compliant fixture; add more only where each tests something the others do not. Mark each line " +
		"the query must report with a trailing # $ Alert comment, and leave the compliant examples " +
		"unmarked. A result meets a mark on the line its location ends on, so report the value on that " +
		"line, such as a uses step's getCalleeNode(), not the step or job around it, whose location " +
		"runs on to the next item.\n" +
		"3. Write the .expected beside the .qlref: one row per marked line, as " +
		"| <file>:<startLine>:<startColumn>:<endLine>:<endColumn> | <element> | <message> |, with the " +
		"message the help says the query reports. Columns are 1-based and the end column is inclusive. " +
		"It is what the query must produce; the query's own output never decides it.\n" +
		"4. Call " + ToolTest + ". ABOM runs the tests against the query as it stands and records " +
		"the result, which must fail because the marked results are missing. It also extracts the test " +
		"database.\n" +
		"5. Run codeql_query_run with queryName PrintAST, queryLanguage actions, database set to the " +
		"test database and sourceFiles naming your fixture files, and read which classes model them.\n" +
		"6. Write the query, and compile it with codeql_query_compile until it compiles.\n" +
		"7. Call " + ToolTest + " and read the .actual beside the test; change the query and repeat " +
		"until the tests pass. Change the .expected only where it misstates a location or an element, " +
		"never to take a result the help does not ask for or drop one it does.\n" +
		"8. Stop, and report in a few lines what passes and what does not.\n" +
		"Keep the help current through every step. When the fixtures, the tree or the query's results " +
		"show you something a reader needs, such as a case the query reports or leaves alone, why it " +
		"matters, or a better fix, edit the section it belongs in. Keep each section short and in the " +
		"same form; say what is true of the query, not what you tried. Edit it to make it accurate, " +
		"never to narrow what the request asks the query to report.\n")
	return out.String()
}

// prompt is the request as the operator wrote it, fenced so none of it reads as an instruction of
// ABOM's.
func prompt(r store.QueryRequest) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Request %d, as the operator wrote it.\n\n", r.RowID)
	if r.Title != "" {
		fmt.Fprintf(&out, "Title:\n%s\n\n", fence(r.Title, "text"))
	}
	fmt.Fprintf(&out, "What to detect:\n%s\n\n", fence(r.Description, "text"))
	for _, side := range []struct{ name, kind, must string }{
		{"Compliant", store.ExampleCompliant, "must not be reported"},
		{"Non-compliant", store.ExampleNonCompliant, "must be reported"},
	} {
		var examples []store.RequestExample
		for _, e := range r.Examples {
			if e.Side == side.kind {
				examples = append(examples, e)
			}
		}
		for i, e := range examples {
			fmt.Fprintf(&out, "%s example %d of %d, which %s", side.name, i+1, len(examples), side.must)
			if link := permalink(e); link != "" {
				fmt.Fprintf(&out, ", lines %d to %d of %s in %s, read at %s", e.StartLine, e.EndLine, e.Path,
					e.Repository, link)
			}
			fmt.Fprintf(&out, ":\n%s\n\n", fence(e.Text, "yaml"))
		}
	}
	if r.Recommendation != "" {
		fmt.Fprintf(&out, "How to fix what it reports:\n%s\n\n", fence(r.Recommendation, "text"))
	}
	if len(r.References) > 0 {
		refs := make([]string, 0, len(r.References))
		for _, ref := range r.References {
			refs = append(refs, ref.Value)
		}
		fmt.Fprintf(&out, "References:\n%s\n\n", fence(strings.Join(refs, "\n"), "text"))
	}
	if declared := metadataAsked(r); declared != "" {
		fmt.Fprintf(&out, "The query is asked to declare:\n%s\n\n", fence(declared, "text"))
	}
	switch r.Kind {
	case store.RequestMissed:
		out.WriteString("It asks about something the query should report and does not.\n")
	case store.RequestFalseAlarm:
		out.WriteString("It asks about something the query reports and should not.\n")
	case store.RequestBoth:
		out.WriteString("It asks about both something missed and a false alarm.\n")
	}
	if r.SourceRunID != 0 {
		fmt.Fprintf(&out, "It was asked from run %d, with %d result(s) picked as evidence.\n", r.SourceRunID,
			len(r.Evidence))
	}
	return out.String()
}

// permalink is where a linked example was read: its file at the commit, not the ref that may move.
func permalink(e store.RequestExample) string {
	u, err := url.Parse(e.URL)
	if e.URL == "" || err != nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/" + e.Repository + "/blob/" + e.Commit + "/" + e.Path,
		Fragment: fmt.Sprintf("L%d-L%d", e.StartLine, e.EndLine)}).String()
}

// metadataAsked is the QLDoc a request asks for, one tag a line, empty when it asks for none.
func metadataAsked(r store.QueryRequest) string {
	want := requested(r)
	var lines []string
	for _, field := range []struct{ tag, value string }{
		{"@description", want.Description}, {"@precision", want.Precision},
		{"@problem.severity", want.ProblemSeverity}, {"@tags", strings.Join(want.Tags, " ")},
	} {
		if field.value != "" {
			lines = append(lines, field.tag+" "+field.value)
		}
	}
	if s := want.SecuritySeverity; s != nil {
		lines = append(lines, fmt.Sprintf("@security-severity %.1f", *s))
	}
	return strings.Join(lines, "\n")
}

// fence wraps text in a code fence longer than any run of backticks inside it.
func fence(text, lang string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	marks := strings.Repeat("`", max(3, longest+1))
	return marks + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + marks
}
