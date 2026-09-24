package harness

import (
	"context"
	"errors"
	"fmt"
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
	// Agent runs a session; nil refuses every start with the reason.
	Agent Agent
	// Model is recorded on each session.
	Model string
	// Lock is held for a session's lifetime: ql-mcp's CodeQL runs take none of ABOM's.
	Lock *runner.Lock
	// Limit is how long one session may run; zero takes DefaultLimit.
	Limit time.Duration
	// Wait is how long a start waits for CodeQL to be free before refusing.
	Wait time.Duration

	mu   sync.Mutex
	live map[int64]*live
}

// DefaultLimit bounds a session nobody configured a limit for.
const DefaultLimit = 45 * time.Minute

type live struct {
	session int64
	gate    *Gate
	cancel  context.CancelFunc
	stopped bool
}

// Start begins a session on a request and answers once it is under way; the agent works on in
// the background, and its timeline is the store's.
func (s *Sessions) Start(request int64, by string) (store.AuthoringSession, error) {
	session, _, err := s.start(request, by)
	return session, err
}

func (s *Sessions) start(request int64, by string) (store.AuthoringSession, <-chan struct{}, error) {
	if s.Agent == nil {
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
	dir := filepath.Join(s.WorkDir, fmt.Sprintf("session-%d", id))
	home, scratch := filepath.Join(dir, "copilot"), filepath.Join(dir, "ql-mcp")
	for _, d := range []string{home, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			unlock()
			_ = s.Store.EndAuthoringSession(id, store.SessionFailed, "preparing the session's directory: "+err.Error())
			return store.AuthoringSession{}, nil, err
		}
	}
	if bounds, err = bounds.Deny(home); err == nil {
		bounds, err = bounds.Read(scratch)
	}
	if err != nil {
		unlock()
		_ = s.Store.EndAuthoringSession(id, store.SessionFailed, "bounding the session: "+err.Error())
		return store.AuthoringSession{}, nil, err
	}

	gate := NewGate(bounds, s.recorder(id))
	if err := gate.KeepPrinted(scratch, s.keeper(id, request)); err != nil {
		unlock()
		_ = s.Store.EndAuthoringSession(id, store.SessionFailed, err.Error())
		return store.AuthoringSession{}, nil, err
	}
	limit := s.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	l := &live{session: id, gate: gate, cancel: cancel}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[int64]*live{}
	}
	s.live[request] = l
	s.mu.Unlock()

	setup := Setup{
		Home:      home,
		Scratch:   scratch,
		Workspace: bounds.Workspace(),
		Prompt:    prompt(req),
		Rules:     rules(bounds, s.Libraries, req.QueryID),
		Gate:      gate,
		Tools:     s.tools(request, gate),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer unlock()
		final, err := s.run(ctx, setup)
		state, detail := s.outcome(ctx, l, limit, final, err)
		cancel()
		s.mu.Lock()
		delete(s.live, request)
		s.mu.Unlock()
		_ = os.RemoveAll(dir)
		_ = s.Store.EndAuthoringSession(id, state, detail)
	}()

	session, err := s.session(request, id)
	return session, done, err
}

// run keeps a panicking agent from taking the server with it.
func (s *Sessions) run(ctx context.Context, setup Setup) (final string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the session failed: %v", r)
		}
	}()
	return s.Agent.Run(ctx, setup)
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

// keeper keeps a printout against the request's query as it stands, and the fixtures it was
// printed from, so a tree printed before they changed reads as old.
func (s *Sessions) keeper(session, request int64) func(Printed) (int64, error) {
	return func(p Printed) (int64, error) {
		req, err := s.requestNamed(request)
		if err != nil {
			return 0, err
		}
		var digest string
		if req.QueryID != "" {
			if q, found, err := authoring.Find(s.Workspace, req.QueryID); err == nil && found {
				digest, _ = authoring.DigestTests(s.Workspace, q.Tests)
			}
		}
		return s.Store.RecordAuthoringAST(store.AuthoringAST{
			SessionID: session, QueryID: req.QueryID, PrintedAt: p.At.UTC().Format(time.RFC3339Nano),
			Query: p.Query, Database: p.Database, SourceFiles: p.SourceFiles, TestDigest: digest,
			Body: p.Body, Truncated: p.Truncated,
		})
	}
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
	query, tests, err := s.locate(req.QueryID)
	if err != nil {
		return Bounds{}, err
	}
	return b.Bind(query, tests)
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

func (s *Sessions) tools(request int64, gate *Gate) []copilot.Tool {
	catalog := copilot.DefineTool(ToolCatalog,
		"Lists the queries ABOM knows: those in your pack, which a session can improve, and those "+
			"it has run from other packs, which it cannot change.",
		func(_ struct{}, _ copilot.ToolInvocation) (string, error) { return s.catalog() })
	target := copilot.DefineTool(ToolTarget,
		"Decides the query this request is about, once: improve one in your pack, or create a new "+
			"one. ABOM records the decision and lays out the query's files, and the session may then "+
			"write that query and nothing else.",
		func(c targetChoice, _ copilot.ToolInvocation) (string, error) { return s.decide(request, gate, c) })
	catalog.SkipPermission, target.SkipPermission = true, true
	return []copilot.Tool{catalog, target}
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
		if err := s.Store.DecideTarget(request, c.QueryID, c.Rationale); err != nil {
			return "", err
		}
		var err error
		if query, tests, err = customql.ScaffoldQuery(s.Workspace, c.Name, c.QueryID, request); err != nil {
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
		"since create refuses a file that exists. Write fixtures under %s, run its test with tests "+
		"[%q], and extract into %s.", c.QueryID, b.query, filepath.Join(b.tests, ".github", "workflows"),
		b.tests, b.database()), nil
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
		"view, glob and grep; write with create and edit; use ql-mcp's %s; and ABOM's %s and %s.\n\n",
		strings.Join(libraries, ", "), strings.Join(loopNames(), ", "), ToolCatalog, ToolTarget)
	if b.bound() {
		fmt.Fprintf(&out, "This session is about %s, the query in %s, tested in %s.\n\n", target, b.query,
			b.tests)
	} else {
		fmt.Fprintf(&out, "No query has been chosen for this request. Call %s, then %s: improve a "+
			"query already in your pack, or create a new one with a new id and file name. Nothing can "+
			"be written until you have decided.\n\n", ToolCatalog, ToolTarget)
	}
	out.WriteString("You may write the target query, its help in a .md file beside it, and Actions " +
		"YAML fixtures under its test directory: workflows under .github/workflows there, or action " +
		"metadata. create writes a new file and refuses one that exists; change a file with edit. " +
		"The .qlref, the .expected and .actual files, the test database and every manifest " +
		"belong to ABOM and the CLI.\n\n")
	out.WriteString("Work test-first, in this order:\n" +
		"1. Write fixtures from the request's examples, marking each case with a # NON_COMPLIANT or " +
		"# COMPLIANT comment. The non-compliant example must be reported and the compliant one must not.\n" +
		"2. Extract the test database with codeql_test_extract and tests set to the test directory.\n" +
		"3. Run codeql_query_run with queryName PrintAST, queryLanguage actions, database set to the " +
		"test database and sourceFiles naming your fixture files, and read which classes model them.\n" +
		"4. Write the query, and compile it with codeql_query_compile.\n" +
		"5. Run codeql_test_run with tests set to the test directory and read the .actual file; repeat " +
		"4 and 5 until it reports the non-compliant cases and none of the compliant ones.\n" +
		"6. Write the help.\n" +
		"7. Ask for the output to be accepted with codeql_test_accept. An operator reads it against " +
		"the request before it becomes the expected result, and this session does not accept it. " +
		"After the answer, stop, and report what each step produced.\n")
	return out.String()
}

// prompt is the request as the operator wrote it, fenced so none of it reads as an instruction
// of ABOM's.
func prompt(r store.QueryRequest) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Request %d, as the operator wrote it.\n\nWhat to detect:\n%s\n\n", r.RowID,
		fence(r.Description, "text"))
	fmt.Fprintf(&out, "Compliant example, which must not be reported:\n%s\n\n", fence(r.Compliant, "yaml"))
	fmt.Fprintf(&out, "Non-compliant example, which must be reported:\n%s\n\n", fence(r.NonCompliant, "yaml"))
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
