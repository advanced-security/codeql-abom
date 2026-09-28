package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// journal is a recorder that keeps every event, and can be told to fail.
type journal struct {
	mu     sync.Mutex
	events []Event
	fail   error
}

func (j *journal) record(e Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail != nil {
		return j.fail
	}
	j.events = append(j.events, e)
	return nil
}

func (j *journal) all() []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Event(nil), j.events...)
}

func newGate(t *testing.T) (*Gate, *journal, place) {
	t.Helper()
	p := newPlace(t)
	j := &journal{}
	return NewGate(p.bound(t), j.record), j, p
}

// Every hook invocation becomes one event, so the timeline can answer what the agent tried as well
// as what it did.
func TestEveryRecordedHookInvocationBecomesOneEvent(t *testing.T) {
	g, j, p := newGate(t)
	r := p.recording(t)

	for _, c := range r.Calls {
		if _, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: c.Tool, ToolArgs: c.Args},
			copilot.HookInvocation{}); err != nil {
			t.Fatalf("%s: a hook returned an error", c.Tool)
		}
	}
	for _, raw := range r.Permissions {
		if _, err := g.Permission(request(t, raw), copilot.PermissionInvocation{}); err != nil {
			t.Fatalf("a permission returned an error")
		}
	}
	events := j.all()
	if got, want := len(events), len(r.Calls)+len(r.Permissions); got != want {
		t.Fatalf("%d events for %d hook invocations", got, want)
	}
	for _, e := range events {
		switch {
		case e.Hook == "" || e.Tool == "" || e.Decision == "":
			t.Errorf("event without hook, tool or decision: %+v", e)
		case e.Decision == DecisionRefused && e.Reason == "":
			t.Errorf("refusal of %s without a reason", e.Tool)
		// A shell command is kept as typed: it is the evidence of what was attempted.
		case e.Tool != "bash" && strings.Contains(e.Subject, p.ws):
			t.Errorf("subject %q names the pack by its machine path", e.Subject)
		}
	}
}

// The refusals are the security record: an attempt that never ran is still an attempt.
func TestARefusedAttemptIsKeptWithWhatWasAttempted(t *testing.T) {
	g, j, _ := newGate(t)
	out, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: "bash",
		ToolArgs: map[string]any{"command": "rm -rf ~", "description": "tidy"}}, copilot.HookInvocation{})
	if err != nil || out == nil || out.PermissionDecision != "deny" {
		t.Fatalf("shell: %+v %v, want denied", out, err)
	}
	e := j.all()[0]
	if e.Decision != DecisionRefused || !strings.Contains(e.Detail, "rm -rf ~") {
		t.Errorf("refused shell recorded as %+v", e)
	}
}

// File contents are the agent's working text, which the timeline has no use for and a reader has
// no need to wade through; the file it named and the size are what an audit asks.
func TestTheTimelineKeepsWhatWasTouchedNotTheText(t *testing.T) {
	g, j, p := newGate(t)
	body := strings.Repeat("jobs: {}\n", 500)
	path := filepath.Join(p.ws, "test", "CheckoutTagPin", ".github", "workflows", "w.yml")
	if _, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: "create",
		ToolArgs: map[string]any{"path": path, "file_text": body}}, copilot.HookInvocation{}); err != nil {
		t.Fatal(err)
	}
	e := j.all()[0]
	if e.Subject != "test/CheckoutTagPin/.github/workflows/w.yml" {
		t.Errorf("subject %q, want the path in the pack", e.Subject)
	}
	if strings.Contains(e.Detail, "jobs:") || len(e.Detail) > maxDetail {
		t.Errorf("detail keeps the text: %q", e.Detail)
	}
}

func TestAnApprovedCodeQLCallIsUnderWayUntilItsResultArrives(t *testing.T) {
	g, j, p := newGate(t)
	clock := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	g.Now = func() time.Time { return clock }
	args := map[string]any{"tests": []any{filepath.Join(p.ws, "test", "CheckoutTagPin")}}

	d, err := g.Permission(&rpc.PermissionRequestMCP{ServerName: Server,
		ToolName: Server + "-codeql_test_run", Args: args}, copilot.PermissionInvocation{})
	if _, ok := d.(*rpc.PermissionDecisionApproveOnce); !ok || err != nil {
		t.Fatalf("test run: %T %v, want approved", d, err)
	}
	flights := g.InFlight()
	if len(flights) != 1 || flights[0].Tool != Server+"-codeql_test_run" ||
		flights[0].Subject != "test/CheckoutTagPin" || !flights[0].Since.Equal(clock) {
		t.Fatalf("under way: %+v", flights)
	}

	clock = clock.Add(8 * time.Second)
	if _, err := g.PostToolUse(copilot.PostToolUseHookInput{ToolName: Server + "-codeql_test_run",
		ToolArgs: args, ToolResult: map[string]any{"resultType": "success", "textResultForLlm": "1 of 1 passed"}},
		copilot.HookInvocation{}); err != nil {
		t.Fatal(err)
	}
	if left := g.InFlight(); len(left) != 0 {
		t.Errorf("still under way after its result: %+v", left)
	}
	last := j.all()[len(j.all())-1]
	if last.Decision != DecisionSucceeded || last.Took != 8*time.Second || last.Step != StepTests {
		t.Errorf("result recorded as %+v", last)
	}
}

func TestARefusedOrFailedCallIsNotLeftUnderWay(t *testing.T) {
	g, _, p := newGate(t)
	if _, err := g.Permission(&rpc.PermissionRequestMCP{ServerName: Server,
		ToolName: Server + "-codeql_pack_install", Args: map[string]any{}}, copilot.PermissionInvocation{}); err != nil {
		t.Fatal(err)
	}
	if left := g.InFlight(); len(left) != 0 {
		t.Fatalf("a refused call is under way: %+v", left)
	}
	args := map[string]any{"query": filepath.Join(p.ws, "src", "CheckoutTagPin.ql")}
	if _, err := g.Permission(&rpc.PermissionRequestMCP{ServerName: Server,
		ToolName: Server + "-codeql_query_compile", Args: args}, copilot.PermissionInvocation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PostToolUseFailure(copilot.PostToolUseFailureHookInput{
		ToolName: Server + "-codeql_query_compile", ToolArgs: args, Error: "exit 2"}, copilot.HookInvocation{}); err != nil {
		t.Fatal(err)
	}
	if left := g.InFlight(); len(left) != 0 {
		t.Errorf("a failed call is under way: %+v", left)
	}
}

// The SDK answers a hook's error to the CLI as a JSON-RPC fault and does not say what the CLI then
// does, so a gate that breaks must answer with a refusal of its own.
func TestAGateThatBreaksRefusesAndNeverReturnsAnError(t *testing.T) {
	g, j, p := newGate(t)
	g.call = func(Bounds, string, any) Verdict { panic("broken") }
	g.request = func(Bounds, rpc.PermissionRequest) Verdict { panic("broken") }

	out, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: "view",
		ToolArgs: map[string]any{"path": p.ws}}, copilot.HookInvocation{})
	if err != nil || out == nil || out.PermissionDecision != "deny" {
		t.Errorf("pre-tool-use: %+v %v, want a denial", out, err)
	}
	d, err := g.Permission(&rpc.PermissionRequestRead{Path: p.ws}, copilot.PermissionInvocation{})
	if _, ok := d.(*rpc.PermissionDecisionReject); !ok || err != nil {
		t.Errorf("permission: %T %v, want a rejection", d, err)
	}
	for _, e := range j.all() {
		if e.Decision != DecisionRefused || !strings.Contains(e.Reason, "broken") {
			t.Errorf("a broken gate recorded %+v", e)
		}
	}
}

// A call nobody can audit does not run: a timeline that cannot be written refuses what it could not
// record, and says so to the agent.
func TestACallTheTimelineCannotRecordIsRefused(t *testing.T) {
	g, j, p := newGate(t)
	j.fail = errors.New("disk full")
	out, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: "view",
		ToolArgs: map[string]any{"path": p.ws}}, copilot.HookInvocation{})
	if err != nil || out == nil || out.PermissionDecision != "deny" ||
		!strings.Contains(out.PermissionDecisionReason, "disk full") {
		t.Errorf("unrecordable read: %+v %v, want a denial naming why", out, err)
	}
	d, err := g.Permission(&rpc.PermissionRequestRead{Path: p.ws}, copilot.PermissionInvocation{})
	if _, ok := d.(*rpc.PermissionDecisionReject); !ok || err != nil {
		t.Errorf("unrecordable permission: %T %v, want a rejection", d, err)
	}
}

func TestAcceptingIsAskedOfAnOperatorAndRefusedUntilOneAnswers(t *testing.T) {
	g, j, p := newGate(t)
	args := map[string]any{"tests": []any{filepath.Join(p.ws, "test", "CheckoutTagPin", "CheckoutTagPin.qlref")}}
	out, err := g.PreToolUse(copilot.PreToolUseHookInput{ToolName: Server + "-codeql_test_accept",
		ToolArgs: args}, copilot.HookInvocation{})
	if err != nil || out == nil || out.PermissionDecision != "ask" {
		t.Fatalf("test accept: %+v %v, want asked", out, err)
	}
	d, err := g.Permission(&rpc.PermissionRequestHook{ToolName: Server + "-codeql_test_accept",
		ToolArgs: args}, copilot.PermissionInvocation{})
	reject, ok := d.(*rpc.PermissionDecisionReject)
	if !ok || err != nil || reject.Feedback == nil || !strings.Contains(*reject.Feedback, "operator") {
		t.Fatalf("hook request: %T %v, want a rejection that says an operator decides", d, err)
	}
	for _, e := range j.all() {
		if e.Decision != DecisionReview {
			t.Errorf("accept recorded as %q, want %q", e.Decision, DecisionReview)
		}
	}
}

// The target is decided mid-session; every hook after that judges by the new bounds.
func TestBindingATargetWidensOnlyWhatComesAfter(t *testing.T) {
	p := newPlace(t)
	b, err := NewBounds(p.ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGate(b, (&journal{}).record)
	write := &rpc.PermissionRequestWrite{FileName: filepath.Join(p.ws, "src", "CheckoutTagPin.ql")}
	if d, _ := g.Permission(write, copilot.PermissionInvocation{}); isApproved(d) {
		t.Fatal("write approved before a target")
	}
	if err := g.Bind(filepath.Join(p.ws, "src", "CheckoutTagPin.ql"),
		filepath.Join(p.ws, "test", "CheckoutTagPin")); err != nil {
		t.Fatal(err)
	}
	if d, _ := g.Permission(write, copilot.PermissionInvocation{}); !isApproved(d) {
		t.Error("write refused after the target was bound")
	}
	if err := g.Bind(filepath.Join(p.ws, "src", "Other.ql"),
		filepath.Join(p.ws, "test", "Other")); err == nil {
		t.Error("a second target bound; a session is about one query")
	}
}

func TestTheHooksTheSessionNeedsAreAllWired(t *testing.T) {
	g, _, _ := newGate(t)
	h := g.Hooks()
	if h.OnPreToolUse == nil || h.OnPostToolUse == nil || h.OnPostToolUseFailure == nil ||
		h.OnErrorOccurred == nil || h.OnSessionStart == nil || h.OnSessionEnd == nil {
		t.Fatalf("hooks left unwired: %+v", h)
	}
	out, err := h.OnErrorOccurred(copilot.ErrorOccurredHookInput{Error: "model unavailable",
		ErrorContext: "model_call", Recoverable: true}, copilot.HookInvocation{})
	if err != nil || out == nil || out.ErrorHandling != "abort" {
		t.Errorf("error: %+v %v, want the session aborted rather than retried unseen", out, err)
	}
}

func isApproved(d rpc.PermissionDecision) bool {
	_, ok := d.(*rpc.PermissionDecisionApproveOnce)
	return ok
}

// Events cross into the store and the API as JSON, so the shape is part of the contract.
func TestAnEventSaysItsDurationInMilliseconds(t *testing.T) {
	body, err := json.Marshal(Event{Hook: HookPostToolUse, Took: 1500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"tookMs":1500`) {
		t.Errorf("event JSON %s", body)
	}
}

// A session whose ql-mcp never started offers the agent none of its tools and reports nothing; a
// recorded one said only that the tools were missing. The timeline says why, and what the agent said.
func TestTheAgentsWordsAndItsServersReachTheTimeline(t *testing.T) {
	g, j, _ := newGate(t)
	failure := "MCP server process exited before completing the MCP initialize handshake (exit code 1)"
	for _, data := range []copilot.SessionEventData{
		&copilot.SessionMCPServerStatusChangedData{ServerName: Server, Status: copilot.MCPServerStatusFailed,
			Error: &failure},
		&copilot.AssistantMessageData{Content: "Extracting the test database first."},
		// A message that only calls tools says nothing, and its calls are recorded as calls.
		&copilot.AssistantMessageData{Content: "  "},
		&copilot.SessionIdleData{},
	} {
		g.Observe(copilot.SessionEvent{Data: data})
	}

	events := j.all()
	if len(events) != 2 {
		t.Fatalf("recorded %d events, want the server's failure and the agent's words: %+v", len(events), events)
	}
	if e := events[0]; e.Hook != HookServer || e.Tool != Server || e.Subject != "failed" ||
		e.Detail != failure || e.Decision != DecisionNoted {
		t.Errorf("server event %+v", e)
	}
	if e := events[1]; e.Hook != HookMessage || e.Detail != "Extracting the test database first." ||
		e.Decision != DecisionNoted {
		t.Errorf("message event %+v", e)
	}
}

// A session's cost arrives one model call at a time, and each is kept as it arrives, with what the
// CLI did not report left unreported. A cost that cannot be kept is said on the timeline.
func TestWhatEachModelCallCostIsKeptAsItArrives(t *testing.T) {
	g, j, _ := newGate(t)
	var kept []Spend
	g.KeepSpend(func(s Spend) error {
		kept = append(kept, s)
		return nil
	})
	in, out := int64(1200), int64(80)
	g.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "claude-opus-5",
		InputTokens: &in, OutputTokens: &out,
		CopilotUsage: &copilot.AssistantUsageCopilotUsage{TotalNanoAiu: 2.5e8}}})
	g.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "claude-opus-5", InputTokens: &in}})

	if len(kept) != 2 {
		t.Fatalf("kept %d, want one per model call: %+v", len(kept), kept)
	}
	if k := kept[0]; k.InputTokens == nil || *k.InputTokens != 1200 || k.OutputTokens == nil ||
		*k.OutputTokens != 80 || k.NanoAIU == nil || *k.NanoAIU != 250_000_000 {
		t.Errorf("first call kept %+v", k)
	}
	if k := kept[1]; k.InputTokens == nil || k.OutputTokens != nil || k.NanoAIU != nil {
		t.Errorf("a call that reported only its input kept %+v", k)
	}
	if events := j.all(); len(events) != 0 {
		t.Errorf("a kept cost reached the timeline: %+v", events)
	}

	g.KeepSpend(func(Spend) error { return errors.New("the store is closed") })
	g.Observe(copilot.SessionEvent{Data: &copilot.AssistantUsageData{Model: "claude-opus-5", InputTokens: &in}})
	if events := j.all(); len(events) != 1 || events[0].Hook != HookSpend ||
		!strings.Contains(events[0].Detail, "the store is closed") {
		t.Errorf("a cost that could not be kept left %+v, want it said on the timeline", events)
	}
}

// printout lays out what ql-mcp writes for a tool query run, and answers the way it answered live.
func printout(t *testing.T, scratch, body string) string {
	t.Helper()
	path := filepath.Join(scratch, "query-logs", "query-run-1", "results.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return "\n\nQuery results interpreted successfully with format: graphtext\n" +
		"Interpreted output saved to: " + path + "\n\nWarnings/Info:\nWriting logs to " +
		filepath.Dir(path) + "/query-run.log.\n"
}

func printAST(g *Gate, p place, result string) {
	g.PostToolUse(copilot.PostToolUseHookInput{ToolName: Server + "-codeql_query_run",
		ToolArgs: map[string]any{"queryName": "PrintAST", "queryLanguage": "actions",
			"database":    filepath.Join(p.ws, "test", "CheckoutTagPin", "CheckoutTagPin.testproj"),
			"sourceFiles": ".github/workflows/checkout.yml"},
		ToolResult: map[string]any{"textResultForLlm": result}}, copilot.HookInvocation{})
}

// The scratch PrintAST prints into is scrubbed with the session, so the tree is kept as the call
// lands, from the file ql-mcp names, and the timeline says it was.
func TestWhatPrintASTPrintedIsKeptPastTheSession(t *testing.T) {
	g, j, p := newGate(t)
	scratch := filepath.Join(p.root, "scratch")
	var kept []Printed
	if err := g.KeepPrinted(scratch, func(pr Printed) (int64, error) {
		kept = append(kept, pr)
		return 7, nil
	}); err == nil {
		t.Error("kept printouts from a scratch directory that does not exist")
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := g.KeepPrinted(scratch, func(pr Printed) (int64, error) {
		kept = append(kept, pr)
		return 7, nil
	}); err != nil {
		t.Fatal(err)
	}
	tree := "#10000 UsesStep\n  #10001 ScalarValue actions/checkout@v4\n"
	printAST(g, p, printout(t, scratch, tree))

	if len(kept) != 1 {
		t.Fatalf("kept %d printouts", len(kept))
	}
	if pr := kept[0]; pr.Body != tree || pr.Query != "PrintAST" || pr.Truncated ||
		pr.Database != "test/CheckoutTagPin/CheckoutTagPin.testproj" ||
		pr.SourceFiles != ".github/workflows/checkout.yml" || pr.At.IsZero() {
		t.Errorf("kept %+v", pr)
	}
	var noted bool
	for _, e := range j.all() {
		noted = noted || (e.Hook == HookArtifact && e.Step == StepAST && strings.Contains(e.Detail, "7"))
	}
	if !noted {
		t.Errorf("the timeline does not say the tree was kept: %+v", j.all())
	}
}

// ql-mcp's answer is text the agent's own call shaped, so the file it names is read only from the
// session's scratch.
func TestAPrintoutIsReadOnlyFromTheSessionsScratch(t *testing.T) {
	g, j, p := newGate(t)
	scratch := filepath.Join(p.root, "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := g.KeepPrinted(scratch, func(Printed) (int64, error) { kept++; return 1, nil }); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(p.root, "secret.txt")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scratch, "query-logs", "query-run-2", "results.txt")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{
		"Interpreted output saved to: " + secret + "\n",
		"Interpreted output saved to: " + link + "\n",
		"Query results interpreted successfully\n",
	} {
		printAST(g, p, result)
	}
	if kept != 0 {
		t.Errorf("kept %d printouts from outside the scratch", kept)
	}
	var refusals int
	for _, e := range j.all() {
		if e.Hook == HookArtifact && strings.Contains(e.Reason, "not kept") {
			refusals++
		}
	}
	if refusals != 3 {
		t.Errorf("%d of 3 unkept printouts are on the timeline: %+v", refusals, j.all())
	}

	// A query run that prints nothing of the fixtures is not a printout.
	g.PostToolUse(copilot.PostToolUseHookInput{ToolName: Server + "-codeql_query_run",
		ToolArgs:   map[string]any{"query": filepath.Join(p.ws, "src", "CheckoutTagPin.ql")},
		ToolResult: map[string]any{"textResultForLlm": printout(t, scratch, "x")}}, copilot.HookInvocation{})
	if kept != 0 {
		t.Error("kept the output of the target's own query as a printout")
	}
}

func TestAnOversizedPrintoutIsKeptCutAndSaysSo(t *testing.T) {
	g, _, p := newGate(t)
	scratch := filepath.Join(p.root, "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	var kept Printed
	if err := g.KeepPrinted(scratch, func(pr Printed) (int64, error) { kept = pr; return 1, nil }); err != nil {
		t.Fatal(err)
	}
	printAST(g, p, printout(t, scratch, strings.Repeat("é", maxPrinted)))
	if !kept.Truncated || len(kept.Body) > maxPrinted || !utf8.ValidString(kept.Body) {
		t.Errorf("kept %d bytes, truncated %v, valid %v", len(kept.Body), kept.Truncated, utf8.ValidString(kept.Body))
	}
}

// graphtext output is a directory holding one file per query, named for its @id: what ql-mcp calls
// results.txt is that directory.
func TestAPrintoutDirectoryIsKeptFileByFileAndTheAgentIsToldWhereTheTreeIs(t *testing.T) {
	g, _, p := newGate(t)
	scratch := filepath.Join(p.root, "scratch")
	results := filepath.Join(scratch, "query-logs", "query-run-1", "results.txt")
	tree := filepath.Join(results, "actions", "tools", "print-ast.txt")
	if err := os.MkdirAll(filepath.Dir(tree), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "checkout.yml:\n#    1| [WorkflowImpl] name: checkout\n"
	if err := os.WriteFile(tree, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(p.root, "secret.txt")
	if err := os.WriteFile(outside, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(results, "actions", "leak.txt")); err != nil {
		t.Fatal(err)
	}
	var kept Printed
	if err := g.KeepPrinted(scratch, func(pr Printed) (int64, error) { kept = pr; return 9, nil }); err != nil {
		t.Fatal(err)
	}

	out, err := g.PostToolUse(copilot.PostToolUseHookInput{ToolName: Server + "-codeql_query_run",
		ToolArgs: map[string]any{"queryName": "PrintAST", "queryLanguage": "actions",
			"database": filepath.Join(p.ws, "test", "CheckoutTagPin", "CheckoutTagPin.testproj")},
		ToolResult: map[string]any{"textResultForLlm": "Interpreted output saved to: " + results + "\n"}},
		copilot.HookInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kept.Body, body) || strings.Contains(kept.Body, "token") {
		t.Errorf("kept %q", kept.Body)
	}
	if out == nil || !strings.Contains(out.AdditionalContext, "print-ast.txt") {
		t.Errorf("the agent is not told where the tree is: %+v", out)
	}
}
