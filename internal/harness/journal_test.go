package harness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// course is a session's whole course, scripted: its policy, choosing its query, each link the first
// change waits on, including a run that replaces the one the change was waiting on, the change, what
// it spent and ran on, and the nudges between them.
func course(t *testing.T) stand {
	return func(ctx context.Context, s Setup) (string, error) {
		c := chain{t: t, s: s, ws: s.Workspace}
		if err := s.Gate.startPolicy(ctx, s, &copilot.SessionConfig{Model: "claude-opus-5",
			AvailableTools: []string{"view", "edit"}}, nil); err != nil {
			t.Error(err)
		}
		run := func(name string, args map[string]any) {
			if _, err := call(t, tool(t, s, name), args); err != nil {
				t.Error(err)
			}
		}
		edit := func() {
			if _, err := s.Gate.PreToolUse(copilot.PreToolUseHookInput{ToolName: "edit", ToolArgs: map[string]any{
				"path": s.Gate.Bounds().Query(), "old_str": "x", "new_str": "y"}}, copilot.HookInvocation{}); err != nil {
				t.Error(err)
			}
		}
		s.Next(ctx)
		run(ToolCatalog, map[string]any{})
		run(ToolTarget, map[string]any{"decision": "improve", "queryId": "acme/self-hosted-runner",
			"rationale": "it already reports jobs on self-hosted runners"})
		edit()
		c.help()
		s.Next(ctx)
		c.fixture()
		c.expected()
		run(ToolTest, map[string]any{})
		s.Next(ctx)
		run(ToolTest, map[string]any{})
		edit()
		run(ToolTest, map[string]any{})
		c.print()
		in, out := int64(1200), int64(80)
		s.Gate.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "gpt-5.3-codex",
			InputTokens: &in, OutputTokens: &out,
			CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 2.5e8}}})
		edit()
		s.Next(ctx)
		return "done", nil
	}
}

// coursed lays out what the course works on: a query in the pack with a test, three runs of it, the
// second failing on no marked result, and a request naming no query.
func (b bench) coursed(t *testing.T) int64 {
	t.Helper()
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	*b.next = []runner.TestResult{failing(1, 0), failing(0, 0), failing(1, 0)}
	return b.request(t, "")
}

// replay hands a journal to the store as a coordinator does, a few entries to a report, each report
// following the one before, and ends the session as the reports say it ended.
func replay(t *testing.T, st *store.Store, session int64, entries []store.AuthoringEntry, per int) {
	t.Helper()
	var prev string
	var end *store.SessionEnd
	for seq := int64(1); len(entries) > 0; seq++ {
		n := min(per, len(entries))
		digest := fmt.Sprintf("report-%d", seq)
		got, err := st.IngestAuthoringReport(session, store.AuthoringReport{Seq: seq, Digest: digest,
			Prev: prev, Entries: entries[:n]})
		if err != nil {
			t.Fatalf("report %d: %v", seq, err)
		}
		if got != nil {
			end = got
		}
		prev, entries = digest, entries[n:]
	}
	if end == nil {
		t.Fatal("no report said how the session ended")
	}
	if err := st.EndAuthoringSession(session, end.State, end.Detail); err != nil {
		t.Fatal(err)
	}
}

var instant = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// rowsOf is a table as text, a row a line in the order rows were written, with what differs between
// two runs of one session put aside: when things happened, how long they took, where the bench is on
// disk, and how far a worker's journal was replayed.
func rowsOf(t *testing.T, b bench, table string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(b.place.root, "abom.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{b.place.root}
	if real, err := filepath.EvalSymlinks(b.place.root); err == nil && real != b.place.root {
		roots = append(roots, real)
	}
	slices.SortFunc(roots, func(a, b string) int { return len(b) - len(a) })
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var line []string
		for i, col := range cols {
			if col == "took_ms" || col == "journal_seq" || col == "journal_head" {
				continue
			}
			v := fmt.Sprint(vals[i])
			if raw, ok := vals[i].([]byte); ok {
				v = string(raw)
			}
			for _, root := range roots {
				v = strings.ReplaceAll(v, root, "<bench>")
			}
			line = append(line, col+"="+instant.ReplaceAllString(v, "<instant>"))
		}
		out = append(out, strings.Join(line, " | "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A session a worker runs reads a snapshot of the store and keeps a journal, which the coordinator
// replays a few entries at a time. Run both ways from the same start, the stores end row for row the
// same: what the session read from its journal steered it exactly as the store would have.
func TestASessionKeptInAJournalLeavesTheStoreAsOneKeptLocally(t *testing.T) {
	local := newBench(t, course(t))
	request := local.coursed(t)
	session := local.finish(t, request, "operator")

	remote := newBench(t, course(t))
	// What ql-mcp prints names where it saved it, so both runs save it in the same place.
	remote.sessions.WorkDir = local.sessions.WorkDir
	if got := remote.coursed(t); got != request {
		t.Fatalf("the second bench's request is %d, not %d", got, request)
	}
	id, err := remote.store.StartAuthoringSession(request, "operator", remote.sessions.Model)
	if err != nil || id != session.RowID {
		t.Fatalf("the coordinator started session %d (%v), not %d", id, err, session.RowID)
	}
	snap, err := remote.store.AuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	sent := &reports{}
	tests, err := remote.sessions.Granted(context.Background(), snap, "operator", sent.report)
	if err != nil {
		t.Fatal(err)
	}
	if tests != "test/SelfHostedRunner" {
		t.Errorf("the session answered its test as %q", tests)
	}
	replay(t, remote.store, id, sent.entries, 5)

	kept := map[string]int{}
	for _, table := range []string{"abom_query_request", "abom_authoring_session", "abom_authoring_event",
		"abom_authoring_ast", "abom_query_test", "abom_request_draft"} {
		want, got := rowsOf(t, local, table), rowsOf(t, remote, table)
		kept[table] = len(want)
		if len(want) != len(got) {
			t.Errorf("%s: %d rows kept locally, %d from the journal", table, len(want), len(got))
		}
		for i := range min(len(want), len(got)) {
			if want[i] != got[i] {
				t.Errorf("%s row %d:\nlocally:      %s\nfrom journal: %s", table, i+1, want[i], got[i])
				break
			}
		}
	}
	// Two empty stores agree too; this one has to have kept the whole course.
	if kept["abom_authoring_event"] < 20 || kept["abom_authoring_ast"] != 1 || kept["abom_query_test"] != 1 {
		t.Errorf("the course kept %v, not the whole of it", kept)
	}
	if final := local.store; final != nil {
		sessions, _ := final.AuthoringSessions(request)
		if len(sessions) != 1 || sessions[0].ModelCalls == nil || !strings.Contains(sessions[0].Model, "gpt-5.3-codex") {
			t.Errorf("the course's session reads %+v; want its spend and model kept", sessions)
		}
	}
}

// journaled is a journal of a session the coordinator started on a request about acme/pin.
func journaled(t *testing.T) (bench, *Journal, int64, int64) {
	t.Helper()
	b := newBench(t, nil)
	request := b.request(t, "acme/pin")
	id, err := b.store.StartAuthoringSession(request, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := b.store.AuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	return b, newJournal(snap), request, id
}

// A journal refuses what the store would refuse, at the call the session made, and keeps none of it:
// kept, it would be refused when the coordinator replayed it, failing the session long after the call.
func TestAJournalRefusesWhatTheStoreWouldAtTheCall(t *testing.T) {
	_, j, request, id := journaled(t)
	tree := store.AuthoringAST{SessionID: id, QueryID: "acme/pin", PrintedAt: "2026-11-26T12:00:00Z",
		Query: "PrintAST", Database: "test/Pin/Pin.testproj", Body: "#1 Job a\n", CLIVersion: "2.27.6"}
	for name, write := range map[string]func() error{
		"an event naming no tool": func() error {
			_, err := j.RecordAuthoringEvent(store.AuthoringEvent{SessionID: id, At: "2026-11-26T12:00:00Z",
				Hook: "pre-tool-use", Decision: "allowed"})
			return err
		},
		"an event of another session": func() error {
			_, err := j.RecordAuthoringEvent(store.AuthoringEvent{SessionID: id + 1, At: "2026-11-26T12:00:00Z",
				Hook: "pre-tool-use", Tool: "view", Decision: "allowed"})
			return err
		},
		"a tree naming no CodeQL": func() error {
			t := tree
			t.CLIVersion = ""
			_, err := j.RecordAuthoringAST(t)
			return err
		},
		"a tree of another query": func() error {
			t := tree
			t.QueryID = "acme/other"
			_, err := j.RecordAuthoringAST(t)
			return err
		},
		"a test of no version": func() error {
			return j.RecordQueryTest(store.QueryTest{QueryID: "acme/pin", CLIVersion: "2.27.6"})
		},
		"a test of another query": func() error {
			return j.RecordQueryTest(store.QueryTest{QueryID: "acme/other", QueryVersion: "v1", CLIVersion: "2.27.6"})
		},
		"a second target":      func() error { return j.DecideTarget(request, "acme/other", "it fits better") },
		"a target with no why": func() error { return j.DecideTarget(request, "acme/other", " ") },
		"a draft on a request": func() error {
			_, err := j.RecordRequestDraft(store.RequestDraft{SessionID: id, Description: "d", Compliant: "a",
				NonCompliant: "b"})
			return err
		},
		"spend of another session": func() error { return j.AddAuthoringSpend(id+1, store.AuthoringSpend{}) },
		"no model":                 func() error { return j.SetAuthoringSessionModel(id, "") },
		"an end only a restart makes": func() error {
			return j.EndAuthoringSession(id, store.SessionInterrupted, "the server stopped")
		},
	} {
		if err := write(); err == nil {
			t.Errorf("the journal kept %s", name)
		}
	}
	if kept := j.Take(); len(kept) != 0 {
		t.Errorf("refused writes left %d entries: %+v", len(kept), kept)
	}
}

// What a worker's session keeps is stamped when the session kept it, not when the coordinator
// replayed it, which can be long after.
func TestAJournalStampsWhatItKeepsWhenTheSessionKeptIt(t *testing.T) {
	b, j, request, id := journaled(t)
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	j.Now = func() time.Time { return at }
	if _, err := j.RecordAuthoringEvent(store.AuthoringEvent{SessionID: id, At: "2026-11-26T11:59:00Z",
		Hook: "pre-tool-use", Tool: "view", Decision: "allowed"}); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordQueryTest(store.QueryTest{QueryID: "acme/pin", QueryVersion: "v1", CLIVersion: "2.27.6",
		Total: 1, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.EndAuthoringSession(id, store.SessionFinished, "done"); err != nil {
		t.Fatal(err)
	}
	replay(t, b.store, id, j.Take(), 10)

	page, err := b.store.AuditEvents(store.AuditFilter{Session: id})
	if err != nil || len(page.Items) != 1 || page.Items[0].ReceivedAt != at.Format(time.RFC3339Nano) {
		t.Errorf("the event reads %+v %v; want it received at %s", page.Items, err, at)
	}
	tests, err := b.store.QueryTestsOf("acme/pin")
	if err != nil || len(tests) != 1 || tests[0].RanAt != at.Format(time.RFC3339) {
		t.Errorf("the test reads %+v %v; want it run at %s", tests, err, at)
	}
	if sessions, err := b.store.AuthoringSessions(request); err != nil || len(sessions) != 1 ||
		sessions[0].State != store.SessionFinished {
		t.Errorf("the session reads %+v %v; want it ended as the worker reported", sessions, err)
	}
}

// reports collects what a worker's session reports, in order, and can be made to refuse.
type reports struct {
	mu      sync.Mutex
	entries []store.AuthoringEntry
	batches int
	refuse  func([]store.AuthoringEntry) error
}

func (r *reports) report(entries []store.AuthoringEntry, _ []Flight) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refuse != nil {
		if err := r.refuse(entries); err != nil {
			return err
		}
	}
	r.entries, r.batches = append(r.entries, entries...), r.batches+1
	return nil
}

func (r *reports) count(hook string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.entries {
		if e.Kind == store.EntryEvent && e.Event.Hook == hook {
			n++
		}
	}
	return n
}

func (r *reports) end() *store.SessionEnd {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.entries) - 1; i >= 0; i-- {
		if r.entries[i].Kind == store.EntryEnd {
			return r.entries[i].End
		}
	}
	return nil
}

// granted starts a session on a request about the bench's query the way a coordinator does, and
// answers what a worker needs to run it.
func (b bench) granted(t *testing.T) store.AuthoringSnapshot {
	t.Helper()
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	request := b.request(t, "acme/self-hosted-runner")
	id, err := b.store.StartAuthoringSession(request, "operator", b.sessions.Model)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := b.store.AuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func editQuery(s Setup) *copilot.PreToolUseHookOutput {
	out, _ := s.Gate.PreToolUse(copilot.PreToolUseHookInput{ToolName: "edit", ToolArgs: map[string]any{
		"path": s.Gate.Bounds().Query(), "old_str": "x", "new_str": "y"}}, copilot.HookInvocation{})
	return out
}

// A worker reports a decision the gate makes before the call it decided runs, as the single binary
// records it first: by the time the gate answers, the decision is with the coordinator. What follows
// is reported as it accumulates, the session's end last.
func TestADecisionIsReportedBeforeTheCallItDecidedRuns(t *testing.T) {
	sent := &reports{}
	var atAnswer int
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		editQuery(s)
		atAnswer = sent.count(HookPreToolUse)
		return "done", nil
	}))
	b.sessions.ReportEvery = time.Hour
	if _, err := b.sessions.Granted(context.Background(), b.granted(t), "operator", sent.report); err != nil {
		t.Fatal(err)
	}
	if atAnswer != 1 {
		t.Errorf("%d decisions were reported when the gate answered, want the one it made", atAnswer)
	}
	if end := sent.end(); end == nil || end.State != store.SessionFinished || sent.batches < 2 {
		t.Errorf("the session reported its end as %+v in %d reports", end, sent.batches)
	}
}

// A decision that cannot be reported is a call nobody can audit, so the call is refused and the
// session ends failed, as where the store cannot record it; the decision is not reported later as if
// it had been acted on.
func TestACallWhoseDecisionCannotBeReportedIsRefused(t *testing.T) {
	sent := &reports{}
	var answer *copilot.PreToolUseHookOutput
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		answer = editQuery(s)
		return "done", nil
	}))
	b.sessions.ReportEvery = time.Hour
	unreachable := true
	sent.refuse = func([]store.AuthoringEntry) error {
		if unreachable {
			unreachable = false
			return errors.New("the object store is unreachable")
		}
		return nil
	}
	if _, err := b.sessions.Granted(context.Background(), b.granted(t), "operator", sent.report); err != nil {
		t.Fatal(err)
	}
	if answer == nil || answer.PermissionDecision != "deny" {
		t.Errorf("the gate answered %+v to a call whose decision could not be reported", answer)
	}
	if n := sent.count(HookPreToolUse); n != 0 {
		t.Errorf("%d unreported decisions were reported later", n)
	}
	if end := sent.end(); end == nil || end.State != store.SessionFailed {
		t.Errorf("the session ended %+v; want failed", end)
	}
}

// What a session keeps between decisions is reported as it accumulates; a report that cannot be
// written keeps its entries for the next, so nothing is lost and nothing is reordered.
func TestAReportThatFailsKeepsItsEntriesForTheNext(t *testing.T) {
	sent := &reports{}
	b := newBench(t, stand(func(ctx context.Context, s Setup) (string, error) {
		for range 3 {
			s.Gate.Say("working")
			time.Sleep(30 * time.Millisecond)
		}
		return "done", nil
	}))
	b.sessions.ReportEvery = 10 * time.Millisecond
	failures := 2
	sent.refuse = func([]store.AuthoringEntry) error {
		if failures > 0 {
			failures--
			return errors.New("the object store is unreachable")
		}
		return nil
	}
	if _, err := b.sessions.Granted(context.Background(), b.granted(t), "operator", sent.report); err != nil {
		t.Fatal(err)
	}
	if n := sent.count(HookMessage); n != 3 {
		t.Errorf("%d of the 3 messages were reported", n)
	}
	if end := sent.end(); end == nil || end.State != store.SessionFinished {
		t.Errorf("the session reported its end as %+v", end)
	}
}

// A worker told to stop stops its session, which ends stopped and reports that it did.
func TestAWorkerToldToStopReportsItsSessionStopped(t *testing.T) {
	sent := &reports{}
	ctx, cancel := context.WithCancel(context.Background())
	b := newBench(t, stand(func(agent context.Context, s Setup) (string, error) {
		cancel()
		<-agent.Done()
		return "", agent.Err()
	}))
	if _, err := b.sessions.Granted(ctx, b.granted(t), "operator", sent.report); err != nil {
		t.Fatal(err)
	}
	if end := sent.end(); end == nil || end.State != store.SessionStopped {
		t.Errorf("the stopped session reported its end as %+v", end)
	}
}
