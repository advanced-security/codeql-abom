package execution_test

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/advanced-security/codeql-abom/internal/authoringpool"
	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	authoringID = "spiffe://abom.local/ns/abom/sa/abom-worker-authoring"
	queryID     = "acme/self-hosted-runner"
)

func queryFile(selects string) string {
	return "/**\n * @name SelfHostedRunner\n * @kind problem\n * @id " + queryID + "\n */\nimport actions\n\n" +
		"from Job j select j, \"" + selects + "\"\n"
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// course is a stand-in agent working a request through what a real one reaches: the catalog and the
// target, the help, a fixture and the .expected, a test run, a printed tree, what it spent, and a
// change to the query once everything it is judged by is on record.
type course struct{ t *testing.T }

func (c course) Run(ctx context.Context, s harness.Setup) (string, error) {
	t := c.t
	call := func(name string, args map[string]any) {
		for _, tl := range s.Tools {
			if tl.Name == name {
				if _, err := tl.Handler(copilot.ToolInvocation{ToolName: name, Arguments: args}); err != nil {
					t.Errorf("%s: %v", name, err)
				}
				return
			}
		}
		t.Errorf("no %s tool", name)
	}
	edit := func() {
		if _, err := s.Gate.PreToolUse(copilot.PreToolUseHookInput{ToolName: "edit", ToolArgs: map[string]any{
			"path": s.Gate.Bounds().Query(), "old_str": "x", "new_str": "y"}}, copilot.HookInvocation{}); err != nil {
			t.Error(err)
		}
	}
	s.Next(ctx)
	call(harness.ToolCatalog, map[string]any{})
	call(harness.ToolTarget, map[string]any{"decision": "improve", "queryId": queryID,
		"rationale": "it already reports jobs on self-hosted runners"})
	query, tests := s.Gate.Bounds().Query(), s.Gate.Bounds().Tests()
	edit()
	write(t, filepath.Join(filepath.Dir(query), "SelfHostedRunner.md"), "# Self-hosted runner\n\nReports a job on a self-hosted runner.\n")
	write(t, filepath.Join(tests, ".github", "workflows", "runners.yml"),
		"on: push\njobs:\n  a:\n    runs-on: self-hosted # $ Alert\n    steps:\n      - run: true\n")
	write(t, filepath.Join(tests, "SelfHostedRunner.expected"),
		"| .github/workflows/runners.yml:4:14:4:24 | self-hosted | runs on a self-hosted runner |\n")
	call(harness.ToolTest, map[string]any{})
	s.Next(ctx)
	printed := filepath.Join(s.Scratch, "query-logs", "query-run-1", "results.txt")
	write(t, printed, "#1 Job a\n")
	if _, err := s.Gate.PostToolUse(copilot.PostToolUseHookInput{ToolName: harness.Server + "-codeql_query_run",
		ToolArgs: map[string]any{"queryName": "PrintAST", "queryLanguage": "actions",
			"database": filepath.Join(tests, filepath.Base(tests)+".testproj"), "sourceFiles": ".github/workflows/runners.yml"},
		ToolResult: map[string]any{"textResultForLlm": "\n\nQuery results interpreted successfully with format: graphtext\n" +
			"Interpreted output saved to: " + printed + "\n\nWarnings/Info:\nWriting logs to " + filepath.Dir(printed) +
			"/query-run.log.\n"}}, copilot.HookInvocation{}); err != nil {
		t.Error(err)
	}
	in, out := int64(1200), int64(80)
	s.Gate.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "gpt-5.3-codex",
		InputTokens: &in, OutputTokens: &out, CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 2.5e8}}})
	edit()
	write(t, query, queryFile("runs on a self-hosted runner"))
	s.Next(ctx)
	return "done", nil
}

// side is one place a session runs from: a store, a pack holding the query and its test, and a
// request naming no query.
type side struct {
	root, pack string
	store      *store.Store
	request    int64
}

func newSide(t *testing.T) side {
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
	pack := filepath.Join(root, "custom-queries")
	if _, err := customql.Scaffold(pack, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "src", "SelfHostedRunner.ql"), queryFile("x"))
	if _, err := customql.ScaffoldTest(pack, "SelfHostedRunner"); err != nil {
		t.Fatal(err)
	}
	request, err := st.SaveQueryRequest(store.QueryRequest{Description: "report a job on a self-hosted runner",
		Compliant: "runs-on: ubuntu-latest", NonCompliant: "runs-on: self-hosted", Kind: store.RequestMissed,
		RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return side{root: root, pack: pack, store: st, request: request}
}

// sessions runs the course over workspace, keeping what it does in the side's store.
func (sd side) sessions(t *testing.T, work, workspace string) *harness.Sessions {
	one, zero := int64(1), int64(0)
	return &harness.Sessions{Store: sd.store, Workspace: workspace, WorkDir: work, Agent: course{t},
		Model: "claude-opus-5", Lock: &runner.Lock{}, Limit: time.Minute, Wait: 20 * time.Millisecond,
		Version: func(context.Context) (string, error) { return "2.27.1", nil },
		RunTests: func(context.Context, []string) (runner.TestResult, error) {
			return runner.TestResult{Total: 1, Failed: 1, FailureStage: "RESULT", Missing: &one, Unexpected: &zero,
				Detail: "0 of 1 passed"}, nil
		}}
}

func (sd side) ended(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		sessions, err := sd.store.AuthoringSessions(sd.request)
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) == 1 && sessions[0].State != store.SessionRunning && sessions[0].State != store.SessionQueued {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session is still %+v", sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// relocated unpacks a session somewhere other than the coordinator's pack, since here both share
// one filesystem.
type relocated struct {
	runner *execution.Runner
	to     string
}

func (r relocated) RunSession(ctx context.Context, root string, t execution.SessionTask, in execution.SessionInputs,
	secrets map[string]string, calls execution.SessionCalls) error {
	t.Workspace = r.to
	return r.runner.RunSession(ctx, root, t, in, secrets, calls)
}

var instant = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// ignored are what differ between two runs of one session by where they ran: how long calls took,
// and how a session waited, was asked for and was reported.
func ignored(table, col string) bool {
	return col == "took_ms" || table == "abom_authoring_session" &&
		slices.Contains([]string{"journal_seq", "journal_head", "queued_at", "decision", "attempt", "grant_digest",
			"draft_picks"}, col)
}

func rows(t *testing.T, sd side, roots []string, table string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(sd.root, "abom.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cols, _ := r.Columns()
	var out []string
	for r.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var line []string
		for i, col := range cols {
			if ignored(table, col) {
				continue
			}
			v := fmt.Sprint(vals[i])
			if raw, ok := vals[i].([]byte); ok {
				v = string(raw)
			}
			for _, root := range roots {
				v = strings.ReplaceAll(v, root, "<root>")
			}
			line = append(line, col+"="+instant.ReplaceAllString(v, "<instant>"))
		}
		out = append(out, strings.Join(line, " | "))
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// files are a pack's own files, by path, with what each holds.
func files(t *testing.T, pack string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(pack, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && (d.Name() == ".codeql" || strings.HasSuffix(d.Name(), ".testproj")):
			return filepath.SkipDir
		case !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".actual"):
			return nil
		}
		body, err := os.ReadFile(p)
		rel, _ := filepath.Rel(pack, p)
		out[filepath.ToSlash(rel)] = string(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func resolved(paths ...string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, p)
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			out = append(out, real)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return len(b) - len(a) })
	return out
}

// A request worked by a session in the authoring pool settles exactly as the same request worked by
// the single binary: the coordinator grants a worker what the session reads, the worker runs it and
// reports, and what the coordinator keeps and lands leaves its store and its pack as the session run
// in-process leaves them, row for row and file for file.
func TestASessionInThePoolSettlesAsTheSameSessionRunLocally(t *testing.T) {
	ctx := context.Background()
	work := filepath.Join(t.TempDir(), "authoring")

	local := newSide(t)
	if _, err := local.sessions(t, work, local.pack).Start(local.request, "operator", ""); err != nil {
		t.Fatal(err)
	}
	local.ended(t)

	remote := newSide(t)
	unpacked := filepath.Join(t.TempDir(), "custom-queries")
	ca, err := execution.NewLocalCA("abom.local")
	if err != nil {
		t.Fatal(err)
	}
	point, err := remote.store.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := authz.ReadBundle(filepath.Join(remote.root, "authz"))
	if err != nil {
		t.Fatal(err)
	}
	objects := execution.DirStore(t.TempDir())
	launch := &execution.InProcess{Worker: func(secrets map[string]string) (*execution.Worker, error) {
		id, err := ca.Issue(authoringID, time.Hour)
		if err != nil {
			return nil, err
		}
		key, err := execution.ParseAttemptKey(secrets[execution.AttemptKeySecret])
		if err != nil {
			return nil, err
		}
		return &execution.Worker{Bundle: trust, Identity: id, AttemptKey: key, Objects: objects, Dir: t.TempDir(),
			Session: func(ctx context.Context, task execution.SessionTask, in execution.SessionRead,
				report func([]byte) error) (execution.SessionOutcome, error) {
				var snap store.AuthoringSnapshot
				if err := json.Unmarshal(in.Snapshot, &snap); err != nil {
					return execution.SessionOutcome{}, err
				}
				sessions := remote.sessions(t, work, task.Workspace)
				sessions.Store, sessions.ReportEvery, sessions.Databases = nil, 5*time.Millisecond, in.Databases
				if task.Run != 0 {
					sessions.Agent = drafting{t}
				}
				tests, err := sessions.Granted(ctx, snap, task.By, func(entries []store.AuthoringEntry, inflight []harness.Flight) error {
					body, err := json.Marshal(harness.Reported{Entries: entries, InFlight: inflight})
					if err != nil {
						return err
					}
					return report(body)
				})
				return execution.SessionOutcome{Tests: tests}, err
			}}, nil
	}}
	workers := &execution.Runner{Point: point, Objects: objects, Launch: launch, Roots: ca.Pool(),
		Pool: "authoring", Identity: authoringID, Timeout: time.Minute, ReportPoll: 5 * time.Millisecond}
	pool := &authoringpool.Pool{Store: remote.store, Workers: relocated{workers, unpacked}, Workspace: remote.pack,
		Model: "claude-opus-5", Limit: time.Minute, Concurrency: 1,
		Mint: func(context.Context) (string, time.Time, error) { return "ghs_session", time.Now().Add(time.Hour), nil }}
	decided, err := point.Decide(ctx, authz.Request{Operation: authz.OpAuthoring, Principal: authz.Local(""),
		Subject: authz.RouteSubject("POST /api/query-requests/{id}/sessions"), Allow: true,
		Evidence: map[string]string{"path": fmt.Sprintf("/api/query-requests/%d/sessions", remote.request)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Start(remote.request, "operator", decided.Digest()); err != nil {
		t.Fatal(err)
	}
	remote.ended(t)

	roots := resolved(local.root, remote.root, filepath.Dir(unpacked))
	kept := map[string]int{}
	for _, table := range []string{"abom_query_request", "abom_authoring_session", "abom_authoring_event",
		"abom_authoring_ast", "abom_query_test", "abom_request_draft"} {
		want, got := rows(t, local, roots, table), rows(t, remote, roots, table)
		kept[table] = len(want)
		if len(want) != len(got) {
			t.Errorf("%s: %d rows locally, %d from the pool", table, len(want), len(got))
		}
		for i := range min(len(want), len(got)) {
			if want[i] != got[i] {
				t.Errorf("%s row %d:\nlocally:   %s\nfrom pool: %s", table, i+1, want[i], got[i])
				break
			}
		}
	}
	if kept["abom_authoring_event"] < 15 || kept["abom_authoring_ast"] != 1 || kept["abom_query_test"] != 1 {
		t.Errorf("the course kept %v, not the whole of it", kept)
	}
	want, got := files(t, local.pack), files(t, remote.pack)
	for path, body := range want {
		if got[path] != body {
			t.Errorf("%s reads %q in the pool's pack, %q in the local one", path, got[path], body)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("%s is in the pool's pack and not the local one", path)
		}
	}
	if want[filepath.ToSlash(filepath.Join("src", "SelfHostedRunner.ql"))] != queryFile("runs on a self-hosted runner") {
		t.Error("the session's change to the query did not happen at all")
	}
}

// drafting is a stand-in agent drafting from a run as a real one does: it reads the picked results and
// one picked repository's workflow, records the request it drafted, and says what it spent.
type drafting struct{ t *testing.T }

func (d drafting) Run(_ context.Context, s harness.Setup) (string, error) {
	t := d.t
	call := func(name string, args map[string]any) {
		for _, tl := range s.Tools {
			if tl.Name == name {
				if _, err := tl.Handler(copilot.ToolInvocation{ToolName: name, Arguments: args}); err != nil {
					t.Errorf("%s: %v", name, err)
				}
				return
			}
		}
		t.Errorf("no %s tool", name)
	}
	call(harness.ToolResults, map[string]any{})
	call(harness.ToolSource, map[string]any{"repository": "octo/quiet", "path": ".github/workflows/ci.yml"})
	call(harness.ToolDraft, map[string]any{"description": "report a job on a self-hosted runner",
		"compliant": "runs-on: ubuntu-latest", "nonCompliant": "runs-on: self-hosted",
		"rationale": "the false alarm ran on a hosted runner"})
	in, out := int64(300), int64(40)
	s.Gate.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "gpt-5.3-codex",
		InputTokens: &in, OutputTokens: &out, CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 1e8}}})
	return "Drafted a request from the picks.", nil
}

// sourceDatabase is what a CodeQL database keeps of the source it was built from, as a draft reads it.
func sourceDatabase(t *testing.T, files map[string]string) string {
	t.Helper()
	db := t.TempDir()
	write(t, filepath.Join(db, "codeql-database.yml"), "---\nsourceLocationPrefix: /src/checkout\nprimaryLanguage: actions\n")
	f, err := os.Create(filepath.Join(db, "src.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for rel, body := range files {
		w, err := zw.Create("src/checkout/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(zw.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	return db
}

// run records a run of the side's query over databases, by repository.
func (sd side) run(t *testing.T, databases map[string]string) int64 {
	t.Helper()
	var held []store.ScopedDatabase
	for _, repo := range slices.Sorted(maps.Keys(databases)) {
		held = append(held, store.ScopedDatabase{DatabaseIdentity: store.DatabaseIdentity{Repository: repo,
			Commit: "c1", Mode: store.AnalysisModeRegular}, Status: store.DatabaseRetained, Path: databases[repo]})
	}
	run, err := sd.store.CreateAnalysisRun(store.AnalysisRequest{RequestedBy: "operator",
		Queries: []store.AnalysisQuery{{QueryID: queryID, QueryVersion: "v1"}}, Databases: held})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (sd side) drafted(t *testing.T, run int64) store.DraftSession {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		drafts, err := sd.store.DraftSessions(run)
		if err != nil {
			t.Fatal(err)
		}
		if len(drafts) == 1 && drafts[0].State != store.SessionRunning && drafts[0].State != store.SessionQueued {
			return drafts[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("the draft is still %+v", drafts)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A draft made in the authoring pool settles exactly as the same draft made by the single binary: the
// worker reads the picked repositories' databases as the coordinator handed them over, and what the
// coordinator keeps leaves its store as the draft made in-process leaves it, row for row.
func TestADraftInThePoolSettlesAsTheSameDraftMadeLocally(t *testing.T) {
	ctx := context.Background()
	work := filepath.Join(t.TempDir(), "authoring")
	databases := map[string]string{
		"octo/app":   sourceDatabase(t, map[string]string{".github/workflows/ci.yml": "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n"}),
		"octo/quiet": sourceDatabase(t, map[string]string{".github/workflows/ci.yml": "on: push\njobs:\n  b:\n    runs-on: self-hosted\n"}),
	}
	picks := []harness.Picked{
		{Repository: "octo/app", Kind: store.RequestFalseAlarm, FilePath: ".github/workflows/ci.yml", StartLine: 4,
			Message: "runs on a self-hosted runner"},
		{Repository: "octo/quiet", Kind: store.RequestMissed},
	}

	local := newSide(t)
	localRun := local.run(t, databases)
	sessions := local.sessions(t, work, local.pack)
	sessions.Agent = drafting{t}
	if _, err := sessions.Draft(localRun, queryID, picks, "operator", ""); err != nil {
		t.Fatal(err)
	}
	want := local.drafted(t, localRun)

	remote := newSide(t)
	remoteRun := remote.run(t, databases)
	unpacked := filepath.Join(t.TempDir(), "custom-queries")
	ca, err := execution.NewLocalCA("abom.local")
	if err != nil {
		t.Fatal(err)
	}
	point, err := remote.store.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := authz.ReadBundle(filepath.Join(remote.root, "authz"))
	if err != nil {
		t.Fatal(err)
	}
	objects := execution.DirStore(t.TempDir())
	launch := &execution.InProcess{Worker: func(secrets map[string]string) (*execution.Worker, error) {
		id, err := ca.Issue(authoringID, time.Hour)
		if err != nil {
			return nil, err
		}
		key, err := execution.ParseAttemptKey(secrets[execution.AttemptKeySecret])
		if err != nil {
			return nil, err
		}
		return &execution.Worker{Bundle: trust, Identity: id, AttemptKey: key, Objects: objects, Dir: t.TempDir(), Private: t.TempDir(),
			Session: func(ctx context.Context, task execution.SessionTask, in execution.SessionRead,
				report func([]byte) error) (execution.SessionOutcome, error) {
				var snap store.AuthoringSnapshot
				if err := json.Unmarshal(in.Snapshot, &snap); err != nil {
					return execution.SessionOutcome{}, err
				}
				worker := remote.sessions(t, work, task.Workspace)
				worker.Store, worker.ReportEvery, worker.Databases = nil, 5*time.Millisecond, in.Databases
				worker.Agent = drafting{t}
				tests, err := worker.Granted(ctx, snap, task.By, func(entries []store.AuthoringEntry, inflight []harness.Flight) error {
					body, err := json.Marshal(harness.Reported{Entries: entries, InFlight: inflight})
					if err != nil {
						return err
					}
					return report(body)
				})
				return execution.SessionOutcome{Tests: tests}, err
			}}, nil
	}}
	workers := &execution.Runner{Point: point, Objects: objects, Launch: launch, Roots: ca.Pool(),
		Pool: "authoring", Identity: authoringID, Timeout: time.Minute, ReportPoll: 5 * time.Millisecond}
	pool := &authoringpool.Pool{Store: remote.store, Workers: relocated{workers, unpacked}, Workspace: remote.pack,
		Model: "claude-opus-5", Limit: time.Minute, Concurrency: 1,
		Mint: func(context.Context) (string, time.Time, error) { return "ghs_session", time.Now().Add(time.Hour), nil }}
	decided, err := point.Decide(ctx, authz.Request{Operation: authz.OpAuthoring, Principal: authz.Local(""),
		Subject: authz.RouteSubject("POST /api/runs/{id}/drafts"), Allow: true,
		Evidence: map[string]string{"path": fmt.Sprintf("/api/runs/%d/drafts", remoteRun)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Draft(remoteRun, queryID, picks, "operator", decided.Digest()); err != nil {
		t.Fatal(err)
	}
	got := remote.drafted(t, remoteRun)

	if want.State != store.SessionFinished || want.Draft == nil || got.Draft == nil ||
		got.Draft.Digest != want.Draft.Digest || got.State != want.State {
		t.Fatalf("the pool's draft ended %s with %+v; locally %s with %+v", got.State, got.Draft, want.State, want.Draft)
	}
	roots := resolved(local.root, remote.root, filepath.Dir(unpacked))
	for _, table := range []string{"abom_authoring_session", "abom_authoring_event", "abom_request_draft"} {
		w, g := rows(t, local, roots, table), rows(t, remote, roots, table)
		if len(w) != len(g) {
			t.Errorf("%s: %d rows locally, %d from the pool", table, len(w), len(g))
		}
		for i := range min(len(w), len(g)) {
			if w[i] != g[i] {
				t.Errorf("%s row %d:\nlocally:   %s\nfrom pool: %s", table, i+1, w[i], g[i])
				break
			}
		}
	}
}
