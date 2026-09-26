package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// stand is an agent that does what a test scripts, through the gate and tools it was handed.
type stand func(ctx context.Context, s Setup) (string, error)

func (f stand) Run(ctx context.Context, s Setup) (string, error) { return f(ctx, s) }

type bench struct {
	sessions *Sessions
	store    *store.Store
	place    place
	// next is what the bench's test runner answers, one run at a time.
	next *[]runner.TestResult
}

// benchCLI is the CodeQL the bench says it runs.
const benchCLI = "2.27.1"

func newBench(t *testing.T, agent Agent) bench {
	t.Helper()
	p := newPlace(t)
	if _, err := customql.Scaffold(p.ws, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(p.root, "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	next := &[]runner.TestResult{}
	return bench{
		sessions: &Sessions{
			Store:     st,
			Workspace: p.ws,
			Libraries: []string{filepath.Join(p.codeql, "packages")},
			WorkDir:   filepath.Join(p.root, "work"),
			Agent:     agent,
			Model:     "claude-opus-5",
			Lock:      &runner.Lock{},
			Limit:     time.Minute,
			Wait:      20 * time.Millisecond,
			Version:   func(context.Context) (string, error) { return benchCLI, nil },
			RunTests: func(context.Context, []string) (runner.TestResult, error) {
				if len(*next) == 0 {
					return runner.TestResult{}, errors.New("the bench has no run scripted")
				}
				r := (*next)[0]
				*next = (*next)[1:]
				return r, nil
			},
		},
		store: st,
		place: p,
		next:  next,
	}
}

// failing is a run that stopped at comparing results, with the expectations it counted.
func failing(missing, unexpected int64) runner.TestResult {
	return runner.TestResult{Total: 1, Failed: 1, FailureStage: "RESULT", Missing: &missing,
		Unexpected: &unexpected, Detail: "0 of 1 passed"}
}

func (b bench) request(t *testing.T, target string) int64 {
	t.Helper()
	r := store.QueryRequest{
		Description:  "report actions/checkout pinned to a tag",
		Compliant:    "- uses: actions/checkout@692973e3d937129bcbf40652eb9f2f61becf3332",
		NonCompliant: "- uses: actions/checkout@v4",
		Kind:         store.RequestMissed,
		RequestedBy:  "operator",
	}
	if target != "" {
		r.QueryID, r.TargetSource = target, store.TargetOperator
	}
	id, err := b.store.SaveQueryRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// query writes src/<name>.ql declaring id, and a test naming it unless withTest is false.
func (b bench) query(t *testing.T, name, id string, withTest bool) {
	t.Helper()
	src := "/**\n * @name " + name + "\n * @kind problem\n * @id " + id + "\n */\nimport actions\n\n" +
		"from Job j select j, \"x\"\n"
	if err := os.WriteFile(filepath.Join(b.place.ws, "src", name+".ql"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if withTest {
		if _, err := customql.ScaffoldTest(b.place.ws, name); err != nil {
			t.Fatal(err)
		}
	}
}

func (b bench) finish(t *testing.T, request int64, by string) store.AuthoringSession {
	t.Helper()
	_, done, err := b.sessions.start(request, by)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	got, err := b.store.AuthoringSessions(request)
	if err != nil || len(got) == 0 {
		t.Fatalf("sessions %v %v", got, err)
	}
	return got[0]
}

func TestASessionIsRefusedWhereNoCopilotCLIIsConfigured(t *testing.T) {
	b := newBench(t, nil)
	b.sessions.Agent = nil
	request := b.request(t, "")
	if _, err := b.sessions.Start(request, "operator"); !errors.Is(err, ErrNoAgent) {
		t.Errorf("started without an agent: %v", err)
	}
	if got, _ := b.store.AuthoringSessions(request); len(got) != 0 {
		t.Errorf("a refused start left sessions %+v", got)
	}
}

// The agent writes only into the operator's pack, so a request about a query that lives anywhere
// else is one it cannot work, and saying so beats a session that fails at its first write.
func TestASessionWorksOnlyAQueryInThePack(t *testing.T) {
	b := newBench(t, stand(func(context.Context, Setup) (string, error) { return "", nil }))
	request := b.request(t, "actions/unpinned-tag")
	_, err := b.sessions.Start(request, "operator")
	if err == nil || !strings.Contains(err.Error(), "actions/unpinned-tag") {
		t.Errorf("started on a query outside the pack: %v", err)
	}
	if got, _ := b.store.AuthoringSessions(request); len(got) != 0 {
		t.Errorf("a refused start left sessions %+v", got)
	}
}

func TestAnOperatorsTargetIsBoundBeforeTheAgentStarts(t *testing.T) {
	var seen Setup
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		seen = s
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	b.finish(t, b.request(t, "acme/self-hosted-runner"), "operator")

	bounds := seen.Gate.Bounds()
	if got := bounds.rel(bounds.Query()); got != "src/SelfHostedRunner.ql" {
		t.Errorf("bound to %s", got)
	}
	if got := bounds.rel(bounds.Tests()); got != "test/SelfHostedRunner" {
		t.Errorf("tested in %s", got)
	}
	for _, want := range []string{"src/SelfHostedRunner.ql", "test/SelfHostedRunner", "acme/self-hosted-runner"} {
		if !strings.Contains(seen.Rules, want) {
			t.Errorf("the rules do not name %q", want)
		}
	}
	for _, want := range []string{"report actions/checkout pinned to a tag", "actions/checkout@v4"} {
		if !strings.Contains(seen.Prompt, want) {
			t.Errorf("the prompt does not carry %q", want)
		}
	}
}

func TestAQueryWithNoTestHasOneLaidOutBeforeTheAgentStarts(t *testing.T) {
	var seen Setup
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		seen = s
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", false)
	b.finish(t, b.request(t, "acme/self-hosted-runner"), "operator")

	if _, err := os.Stat(filepath.Join(b.place.ws, "test", "SelfHostedRunner", "SelfHostedRunner.qlref")); err != nil {
		t.Errorf("no test laid out: %v", err)
	}
	if got := seen.Gate.Bounds().rel(seen.Gate.Bounds().Tests()); got != "test/SelfHostedRunner" {
		t.Errorf("tested in %s", got)
	}
}

// What was refused is the security record, and what the agent said is how a reader follows it.
func TestWhatTheAgentTriesIsOnTheTimelineAndTheSessionEndsWithItsWords(t *testing.T) {
	var home string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		home = s.Home
		if _, err := os.Stat(home); err != nil {
			t.Errorf("the session has no home: %v", err)
		}
		s.Gate.PreToolUse(copilot.PreToolUseHookInput{ToolName: "bash",
			ToolArgs: map[string]any{"command": "rm -rf ~"}}, copilot.HookInvocation{})
		s.Gate.PreToolUse(copilot.PreToolUseHookInput{ToolName: "view",
			ToolArgs: map[string]any{"path": filepath.Join(s.Workspace, "src")}}, copilot.HookInvocation{})
		return "Stopped at the review of the expected output.", nil
	}))
	request := b.request(t, "")
	session := b.finish(t, request, "operator")

	if session.State != store.SessionFinished || session.Detail != "Stopped at the review of the expected output." ||
		session.StartedBy != "operator" || session.Model == "" {
		t.Errorf("session %+v", session)
	}
	events, err := b.store.AuthoringEvents(session.RowID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var refusedShell, allowedView bool
	for _, e := range events {
		refusedShell = refusedShell || (e.Tool == "bash" && e.Decision == DecisionRefused && e.Subject == "rm -rf ~")
		allowedView = allowedView || (e.Tool == "view" && e.Decision == DecisionAllowed && e.Subject == "src")
	}
	if !refusedShell || !allowedView {
		t.Errorf("timeline %+v", events)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the session's home outlived it: %v", err)
	}
}

// ql-mcp's CodeQL runs take no lock of ABOM's, so the session holds it: a crawl or a run starting
// underneath would share one compilation cache with the agent's test runs.
func TestASessionHoldsTheCodeQLLockUntilItEnds(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	b := newBench(t, stand(func(context.Context, Setup) (string, error) {
		close(started)
		<-release
		return "done", nil
	}))
	_, done, err := b.sessions.start(b.request(t, ""), "operator")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if unlock, err := b.sessions.Lock.Acquire(ctx); err == nil {
		unlock()
		t.Error("the lock was free while the session ran")
	}
	cancel()
	close(release)
	<-done
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := b.sessions.Lock.Acquire(ctx)
	if err != nil {
		t.Fatalf("the lock was kept after the session ended: %v", err)
	}
	unlock()
}

func TestASessionIsRefusedWhileCodeQLIsBusy(t *testing.T) {
	b := newBench(t, stand(func(context.Context, Setup) (string, error) { return "done", nil }))
	unlock, err := b.sessions.Lock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	request := b.request(t, "")
	if _, err := b.sessions.Start(request, "operator"); !errors.Is(err, ErrBusy) {
		t.Errorf("started while CodeQL was busy: %v", err)
	}
	if got, _ := b.store.AuthoringSessions(request); len(got) != 0 {
		t.Errorf("a refused start left sessions %+v", got)
	}
}

func TestOneSessionAtATimeWorksARequest(t *testing.T) {
	release := make(chan struct{})
	b := newBench(t, stand(func(context.Context, Setup) (string, error) {
		<-release
		return "done", nil
	}))
	request := b.request(t, "")
	_, done, err := b.sessions.start(request, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.sessions.Start(request, "operator"); !errors.Is(err, store.ErrSessionUnderWay) {
		t.Errorf("a second session: %v", err)
	}
	close(release)
	<-done
}

func TestStoppingASessionEndsItStopped(t *testing.T) {
	started := make(chan struct{})
	b := newBench(t, stand(func(ctx context.Context, _ Setup) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}))
	request := b.request(t, "")
	_, done, err := b.sessions.start(request, "operator")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !b.sessions.Stop(request) {
		t.Fatal("nothing to stop")
	}
	<-done
	got, _ := b.store.AuthoringSessions(request)
	if got[0].State != store.SessionStopped || !strings.Contains(got[0].Detail, "stopped") {
		t.Errorf("stopped session %+v", got[0])
	}
	if b.sessions.Stop(request) {
		t.Error("stopped a session that had already ended")
	}
}

func TestASessionPastItsTimeLimitIsStoppedAndSaysSo(t *testing.T) {
	b := newBench(t, stand(func(ctx context.Context, _ Setup) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}))
	b.sessions.Limit = time.Nanosecond
	got := b.finish(t, b.request(t, ""), "operator")
	if got.State != store.SessionStopped || !strings.Contains(got.Detail, "limit") {
		t.Errorf("session %+v", got)
	}
}

func TestAnAgentThatFailsEndsTheSessionFailedWithWhy(t *testing.T) {
	b := newBench(t, stand(func(context.Context, Setup) (string, error) {
		return "", errors.New("the Copilot CLI exited with status 1")
	}))
	got := b.finish(t, b.request(t, ""), "operator")
	if got.State != store.SessionFailed || !strings.Contains(got.Detail, "exited with status 1") {
		t.Errorf("session %+v", got)
	}
}

// A session cannot install packs, and its first test would not compile in a pack whose
// dependencies were never installed, so ABOM installs them before the agent has control, under the
// lock the session holds.
func TestThePackIsInstalledBeforeTheAgentStarts(t *testing.T) {
	var order []string
	b := newBench(t, stand(func(context.Context, Setup) (string, error) {
		order = append(order, "agent")
		return "done", nil
	}))
	b.sessions.Prepare = func(ctx context.Context) error {
		probe, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		if unlock, err := b.sessions.Lock.Acquire(probe); err == nil {
			unlock()
			t.Error("the pack was installed without the session holding the lock")
		}
		order = append(order, "install")
		return nil
	}
	b.finish(t, b.request(t, ""), "operator")
	if got := strings.Join(order, ", "); got != "install, agent" {
		t.Errorf("the session did %s, want the pack installed and then the agent started", got)
	}
}

// A pack that cannot be installed compiles nothing the agent writes, so the session ends failed
// with the reason and the agent never starts.
func TestASessionWhosePackCannotBeInstalledFailsWithWhy(t *testing.T) {
	b := newBench(t, stand(func(context.Context, Setup) (string, error) {
		t.Error("the agent started in a pack that could not be installed")
		return "done", nil
	}))
	b.sessions.Prepare = func(context.Context) error {
		return errors.New("codeql pack install: exit status 1: Failed to download codeql/actions-all@0.6.2")
	}
	got := b.finish(t, b.request(t, ""), "operator")
	if got.State != store.SessionFailed || !strings.Contains(got.Detail, "Failed to download codeql/actions-all@0.6.2") {
		t.Errorf("session %+v, want it failed with the install's reason", got)
	}
}

func tool(t *testing.T, s Setup, name string) copilot.Tool {
	t.Helper()
	for _, tl := range s.Tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no %s tool among %v", name, s.Tools)
	return copilot.Tool{}
}

func call(t *testing.T, tl copilot.Tool, args map[string]any) (copilot.ToolResult, error) {
	t.Helper()
	return tl.Handler(copilot.ToolInvocation{ToolName: tl.Name, Arguments: args})
}

// refusal is why a tool refused, as the agent reads it. The SDK shows the model a handler's error
// only as "Tool execution failed", so a refusal returned that way reaches the agent as nothing: a
// live agent retried one three times without learning why.
func refusal(t *testing.T, tl copilot.Tool, args map[string]any) string {
	t.Helper()
	res, err := call(t, tl, args)
	if err != nil {
		t.Errorf("a refusal was returned as an error, which the agent never reads: %v", err)
		return err.Error()
	}
	if res.ResultType != "failure" {
		return ""
	}
	return res.TextResultForLLM
}

// Nobody chose a target, so the agent does, once: the choice is recorded with its reason, laid out,
// and from then on the only query the session may write.
func TestTheAgentDecidesATargetOnceAndItBindsTheSession(t *testing.T) {
	var bounds Bounds
	var answer, second string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		target := tool(t, s, ToolTarget)
		res, err := call(t, target, map[string]any{"decision": "create", "queryId": "acme/checkout-tag-pin",
			"name": "CheckoutTagPin", "rationale": "nothing in the catalog reports a tag-pinned checkout"})
		if err != nil {
			t.Errorf("deciding: %v", err)
		}
		answer = res.TextResultForLLM
		bounds = s.Gate.Bounds()
		second = refusal(t, target, map[string]any{"decision": "create", "queryId": "acme/other",
			"name": "Other", "rationale": "second thoughts"})
		return "done", nil
	}))
	request := b.request(t, "")
	b.finish(t, request, "operator")

	// The CLI's create refuses a file that exists, and a live agent reached outside the pack
	// looking for another way to write one.
	if !strings.Contains(answer, "edit") {
		t.Errorf("the decision does not say the laid-out query is edited: %q", answer)
	}
	if got := bounds.rel(bounds.Query()); got != "src/CheckoutTagPin.ql" {
		t.Errorf("bound to %s", got)
	}
	if _, err := os.Stat(filepath.Join(b.place.ws, "test", "CheckoutTagPin", "CheckoutTagPin.qlref")); err != nil {
		t.Errorf("no test laid out: %v", err)
	}
	if second == "" {
		t.Error("a second target was decided")
	}
	all, _ := b.store.QueryRequests()
	for _, r := range all {
		if r.RowID == request && (r.QueryID != "acme/checkout-tag-pin" || r.TargetSource != store.TargetAgent ||
			r.TargetDetail == "") {
			t.Errorf("request %+v", r)
		}
	}
}

func TestATargetTheAgentProposesMustBeNewAndPlainlyNamed(t *testing.T) {
	var refusals []string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		target := tool(t, s, ToolTarget)
		for _, args := range []map[string]any{
			{"decision": "create", "queryId": "acme/self-hosted-runner", "name": "Again", "rationale": "r"},
			{"decision": "create", "queryId": "acme/new", "name": "../Escape", "rationale": "r"},
			{"decision": "create", "queryId": "acme/new", "name": "SelfHostedRunner", "rationale": "r"},
			{"decision": "create", "queryId": "not an id", "name": "Fine", "rationale": "r"},
			{"decision": "improve", "queryId": "actions/unpinned-tag", "rationale": "r"},
			{"decision": "create", "queryId": "acme/new", "name": "Fine", "rationale": ""},
			{"decision": "rewrite", "queryId": "acme/new", "name": "Fine", "rationale": "r"},
		} {
			if why := refusal(t, target, args); why != "" {
				refusals = append(refusals, why)
			} else {
				t.Errorf("decided %v", args)
			}
		}
		if s.Gate.Bounds().bound() {
			t.Error("a refused decision bound the session")
		}
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	b.finish(t, b.request(t, ""), "operator")
	for _, r := range refusals {
		if r == "" {
			t.Error("a refusal without a reason")
		}
	}
}

func TestTheCatalogToolListsThePacksQueriesWithTheirFiles(t *testing.T) {
	var listed string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		res, err := call(t, tool(t, s, ToolCatalog), map[string]any{})
		if err != nil {
			t.Error(err)
		}
		listed = res.TextResultForLLM
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	b.finish(t, b.request(t, ""), "operator")
	for _, want := range []string{"acme/self-hosted-runner", "src/SelfHostedRunner.ql", "test/SelfHostedRunner"} {
		if !strings.Contains(listed, want) {
			t.Errorf("the catalog does not list %q:\n%s", want, listed)
		}
	}
}

// The rules ride in the system message, where a request's own text cannot countermand them.
func TestTheRulesStateTheBoundsAndTheReviewGate(t *testing.T) {
	var seen Setup
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		seen = s
		return "done", nil
	}))
	b.finish(t, b.request(t, ""), "operator")
	for _, want := range []string{"no shell", "codeql_test_accept", "sourceFiles", ToolTarget, ToolCatalog,
		ToolTest, "# $ Alert"} {
		if !strings.Contains(seen.Rules, want) {
			t.Errorf("the rules do not say %q:\n%s", want, seen.Rules)
		}
	}
	if strings.Contains(seen.Rules, "actions/checkout@v4") {
		t.Error("the request's text is in the rules")
	}
}

// Sessions so far wrote the query before any test failed, and nothing recorded the order. So the
// query stays as it is until ABOM itself has seen its tests fail on a result the fixtures mark, and
// a failure for any other reason does not count.
func TestTheQuerysFirstChangeWaitsForItsTestsToFailOnAMarkedResult(t *testing.T) {
	var before, afterWrongReason, afterMarked, afterChange string
	var answers []string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		query := s.Gate.Bounds().Query()
		edit := func() string {
			v := s.Gate.Bounds().Call("edit", map[string]any{"path": query, "old_str": "x", "new_str": "y"})
			if v.Allowed {
				return ""
			}
			return v.Reason
		}
		test := tool(t, s, ToolTest)
		before = edit()
		for range 2 {
			res, err := call(t, test, map[string]any{})
			if err != nil {
				t.Error(err)
			}
			answers = append(answers, res.TextResultForLLM)
			if len(answers) == 1 {
				afterWrongReason = edit()
			}
		}
		afterMarked = edit()
		if err := os.WriteFile(query, []byte("/**\n * @kind problem\n * @id acme/self-hosted-runner\n */\n"+
			"import actions\n\nfrom Job j select j, \"changed\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		afterChange = edit()
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	*b.next = []runner.TestResult{failing(0, 0), failing(1, 0)}
	b.finish(t, b.request(t, "acme/self-hosted-runner"), "operator")

	if !strings.Contains(before, ToolTest) {
		t.Errorf("the first change before any run was refused with %q, want it to name %s", before, ToolTest)
	}
	if afterWrongReason == "" {
		t.Error("a failure on no marked result released the query")
	}
	if afterMarked != "" || afterChange != "" {
		t.Errorf("after a failure on a marked result the change was refused: %q, then %q", afterMarked,
			afterChange)
	}
	if len(answers) != 2 || !strings.Contains(answers[0], "not a failure on a marked result") ||
		!strings.Contains(answers[1], "recorded this failure") {
		t.Errorf("abom_test answered %q", answers)
	}
	tests, err := b.store.QueryTestsOf("acme/self-hosted-runner")
	if err != nil || len(tests) != 1 || tests[0].CLIVersion != benchCLI || tests[0].FixturesDigest == "" ||
		tests[0].Missing == nil || *tests[0].Missing != 1 {
		t.Errorf("recorded %+v %v, want the last run under %s with its fixtures", tests, err, benchCLI)
	}
}

// Nothing is tested before the session knows which query it is about.
func TestNothingIsTestedBeforeATargetIsDecided(t *testing.T) {
	var why string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		why = refusal(t, tool(t, s, ToolTest), map[string]any{})
		return "done", nil
	}))
	b.finish(t, b.request(t, ""), "operator")
	if !strings.Contains(why, ToolTarget) {
		t.Errorf("testing with no target answered %q, want it to name %s", why, ToolTarget)
	}
}

// PrintAST prints into ql-mcp's scratch, and a live agent that could not read it went to the
// library sources instead. The CLI's own state beside it holds the session's history and stays
// out of reach, and nothing in either is the agent's to write.
func TestTheAgentReadsWhatQLMCPWroteAndNotTheCLIsState(t *testing.T) {
	var seen Setup
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		seen = s
		results := filepath.Join(s.Scratch, "query-logs", "run-1", "results.txt")
		if err := os.MkdirAll(filepath.Dir(results), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(results, []byte("UsesStep"), 0o600); err != nil {
			t.Fatal(err)
		}
		b := s.Gate.Bounds()
		if v := b.Request(&rpc.PermissionRequestRead{Path: results}); !v.Allowed {
			t.Errorf("reading ql-mcp's results refused: %s", v.Reason)
		}
		if v := b.Request(&rpc.PermissionRequestRead{Path: filepath.Join(s.Home, "session-state")}); v.Allowed {
			t.Error("the CLI's state is readable")
		}
		if v := b.Call("create", map[string]any{"path": results + ".x", "file_text": "x"}); v.Allowed {
			t.Error("ql-mcp's scratch is writable")
		}
		return "done", nil
	}))
	b.finish(t, b.request(t, ""), "operator")
	if seen.Scratch == "" || seen.Home == "" || strings.HasPrefix(seen.Scratch, seen.Home) ||
		strings.HasPrefix(seen.Home, seen.Scratch) {
		t.Errorf("home %q and scratch %q overlap", seen.Home, seen.Scratch)
	}
}

// A reader asks to see the tree the agent read the fixtures as, after the session and its scratch
// are gone; it is kept on the request, against the query and the fixtures it was printed from.
func TestAPrintedTreeOutlivesTheSessionOnTheRequest(t *testing.T) {
	tree := "#10000 UsesStep\n  #10001 ScalarValue actions/checkout@v4\n"
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		bounds := s.Gate.Bounds()
		s.Gate.PostToolUse(copilot.PostToolUseHookInput{ToolName: Server + "-codeql_query_run",
			ToolArgs: map[string]any{"queryName": "PrintAST", "queryLanguage": "actions",
				"database": bounds.database(), "sourceFiles": ".github/workflows/runners.yml"},
			ToolResult: map[string]any{"textResultForLlm": printout(t, s.Scratch, tree)}},
			copilot.HookInvocation{})
		return "done", nil
	}))
	b.query(t, "SelfHostedRunner", "acme/self-hosted-runner", true)
	request := b.request(t, "acme/self-hosted-runner")
	b.finish(t, request, "operator")

	listed, err := b.store.AuthoringASTs(request)
	if err != nil || len(listed) != 1 {
		t.Fatalf("kept %v %v", listed, err)
	}
	got, err := b.store.AuthoringAST(listed[0].RowID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != tree || got.QueryID != "acme/self-hosted-runner" || got.TestDigest == "" ||
		got.Database != "test/SelfHostedRunner/SelfHostedRunner.testproj" || got.CLIVersion != benchCLI {
		t.Errorf("kept %+v", got)
	}
}

// A rejection is the operator's answer to an earlier session, so the next one starts from it; an
// approval since then leaves nothing to answer.
func TestTheNextSessionIsToldWhatAnOperatorRejectedAndWhy(t *testing.T) {
	var prompts []string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		prompts = append(prompts, s.Prompt)
		return "done", nil
	}))
	b.query(t, "Pin", "acme/pin", true)
	request := b.request(t, "acme/pin")
	review := store.AuthoringReview{RequestID: request, QueryID: "acme/pin", QueryVersion: "v1",
		Actual: "| ci.yml:3:9 | pinned to 692973e |\n", Verdict: store.ReviewRejected,
		Reason: "a short SHA is not a pin, so it must be reported", ReviewedBy: "operator"}
	if _, err := b.store.RecordAuthoringReview(review); err != nil {
		t.Fatal(err)
	}
	b.finish(t, request, "operator")
	review.Verdict, review.Reason = store.ReviewApproved, ""
	if _, err := b.store.RecordAuthoringReview(review); err != nil {
		t.Fatal(err)
	}
	b.finish(t, request, "operator")

	if len(prompts) != 2 {
		t.Fatalf("%d sessions ran, want 2", len(prompts))
	}
	for _, want := range []string{"rejected", "a short SHA is not a pin, so it must be reported",
		"| ci.yml:3:9 | pinned to 692973e |"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("the session after a rejection is not told %q:\n%s", want, prompts[0])
		}
	}
	if strings.Contains(prompts[1], "rejected") {
		t.Errorf("the session after an approval is told of the rejection before it:\n%s", prompts[1])
	}
}
