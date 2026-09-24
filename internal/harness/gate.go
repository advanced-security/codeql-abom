package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Hooks an event comes from.
const (
	HookPreToolUse         = "pre-tool-use"
	HookPermission         = "permission"
	HookPostToolUse        = "post-tool-use"
	HookPostToolUseFailure = "post-tool-use-failure"
	HookSessionStart       = "session-start"
	HookSessionEnd         = "session-end"
	HookError              = "error"
	// HookServer and HookMessage are session events rather than hooks: an MCP server changing
	// state, and what the agent said.
	HookServer  = "mcp-server"
	HookMessage = "message"
	// HookArtifact is ABOM keeping something a call produced, such as a printed tree.
	HookArtifact = "artifact"
)

// What an event records: a decision before a call, or its outcome after.
const (
	DecisionAllowed   = "allowed"
	DecisionRefused   = "refused"
	DecisionReview    = "review"
	DecisionSucceeded = "succeeded"
	DecisionFailed    = "failed"
	DecisionNoted     = "noted"
)

// maxDetail bounds what one event keeps of a call's arguments or result.
const maxDetail = 2000

// Event is one hook invocation as the timeline keeps it.
type Event struct {
	At       time.Time `json:"at"`
	Hook     string    `json:"hook"`
	Tool     string    `json:"tool"`
	Subject  string    `json:"subject"`
	Decision string    `json:"decision"`
	Step     string    `json:"step"`
	Reason   string    `json:"reason"`
	Detail   string    `json:"detail"`
	// Took is how long a call ran, from its approval to its result; zero when not measured.
	Took time.Duration `json:"-"`
}

// MarshalJSON writes Took in milliseconds, the unit every other duration in the API uses.
func (e Event) MarshalJSON() ([]byte, error) {
	type plain Event
	return json.Marshal(struct {
		plain
		TookMs int64 `json:"tookMs"`
	}{plain(e), e.Took.Milliseconds()})
}

// Flight is a ql-mcp call the gate approved whose result has not arrived: CodeQL running on the
// agent's behalf, outside ABOM's own runner.
type Flight struct {
	Tool    string    `json:"tool"`
	Subject string    `json:"subject"`
	Step    string    `json:"step"`
	Since   time.Time `json:"since"`
}

// Gate turns Bounds into a session's callbacks and records every invocation of them. No callback
// returns an error or lets a panic out: the SDK answers either to the CLI as a JSON-RPC fault and
// does not say what the CLI then does, so each one refuses instead.
type Gate struct {
	// Now stamps events.
	Now func() time.Time

	mu      sync.Mutex
	bounds  Bounds
	record  func(Event) error
	flights []Flight

	// scratch is where ql-mcp prints, and keepPrinted keeps what it printed there.
	scratch     string
	keepPrinted func(Printed) (int64, error)

	// call and request decide, as fields so a test can break them.
	call    func(Bounds, string, any) Verdict
	request func(Bounds, rpc.PermissionRequest) Verdict
}

// NewGate decides by b, and keeps each event with record before the call it describes may run.
func NewGate(b Bounds, record func(Event) error) *Gate {
	return &Gate{Now: time.Now, bounds: b, record: record, call: Bounds.Call, request: Bounds.Request}
}

// Bind narrows the gate to one target. A session is about one query, so a second is refused.
func (g *Gate) Bind(query, tests string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bounds.bound() {
		return fmt.Errorf("this session is already about %s", g.bounds.rel(g.bounds.query))
	}
	b, err := g.bounds.Bind(query, tests)
	if err != nil {
		return err
	}
	g.bounds = b
	return nil
}

// Bounds is what the gate decides by now.
func (g *Gate) Bounds() Bounds {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bounds
}

// InFlight lists the ql-mcp calls under way, oldest first.
func (g *Gate) InFlight() []Flight {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.flights)
}

// Hooks wires the gate into a session.
func (g *Gate) Hooks() *copilot.SessionHooks {
	return &copilot.SessionHooks{
		OnPreToolUse:         g.PreToolUse,
		OnPostToolUse:        g.PostToolUse,
		OnPostToolUseFailure: g.PostToolUseFailure,
		OnSessionStart:       g.SessionStart,
		OnSessionEnd:         g.SessionEnd,
		OnErrorOccurred:      g.ErrorOccurred,
	}
}

// PreToolUse decides a call as the agent makes it. An allowed call returns no decision, so the
// typed permission request that follows is judged again with what the CLI says it will touch.
func (g *Gate) PreToolUse(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (
	out *copilot.PreToolUseHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = deny(fmt.Sprintf("the gate failed deciding this call, so it is refused: %v", r)), nil
		}
	}()
	b := g.Bounds()
	e := Event{Hook: HookPreToolUse, Tool: in.ToolName, Subject: b.subject(in.ToolName, in.ToolArgs),
		Detail: detail(in.ToolArgs)}
	v := decide(func() Verdict { return g.call(b, in.ToolName, in.ToolArgs) })
	if err := g.keep(e, v); err != nil {
		return deny(unrecorded(err)), nil
	}
	switch {
	case v.Review:
		return &copilot.PreToolUseHookOutput{PermissionDecision: "ask", PermissionDecisionReason: v.Reason}, nil
	case v.Allowed:
		return nil, nil
	}
	return deny(v.Reason), nil
}

// Permission decides a typed permission request, and marks an approved ql-mcp call as under way.
func (g *Gate) Permission(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (
	d rpc.PermissionDecision, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = reject(fmt.Sprintf("the gate failed deciding this request, so it is refused: %v", r)), nil
		}
	}()
	b := g.Bounds()
	tool, subject, args := b.describe(req)
	e := Event{Hook: HookPermission, Tool: tool, Subject: subject, Detail: detail(args)}
	v := decide(func() Verdict { return g.request(b, req) })
	if err := g.keep(e, v); err != nil {
		return reject(unrecorded(err)), nil
	}
	switch {
	case v.Review:
		return reject(v.Reason + "; nothing is accepted in this session, and an operator decides on " +
			"the request"), nil
	case !v.Allowed:
		return reject(v.Reason), nil
	}
	if _, ok := req.(*rpc.PermissionRequestMCP); ok {
		g.mu.Lock()
		g.flights = append(g.flights, Flight{Tool: tool, Subject: subject, Step: v.Step, Since: g.Now()})
		g.mu.Unlock()
	}
	return &rpc.PermissionDecisionApproveOnce{}, nil
}

// PostToolUse records a call's result.
func (g *Gate) PostToolUse(in copilot.PostToolUseHookInput, _ copilot.HookInvocation) (
	out *copilot.PostToolUseHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, nil
		}
	}()
	g.land(HookPostToolUse, in.ToolName, in.ToolArgs, DecisionSucceeded, resultText(in.ToolResult))
	if context := g.print(in.ToolName, in.ToolArgs, resultText(in.ToolResult)); context != "" {
		return &copilot.PostToolUseHookOutput{AdditionalContext: context}, nil
	}
	return nil, nil
}

// PostToolUseFailure records a call that ran and failed.
func (g *Gate) PostToolUseFailure(in copilot.PostToolUseFailureHookInput, _ copilot.HookInvocation) (
	out *copilot.PostToolUseFailureHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, nil
		}
	}()
	g.land(HookPostToolUseFailure, in.ToolName, in.ToolArgs, DecisionFailed, in.Error)
	return nil, nil
}

// SessionStart records how the session began.
func (g *Gate) SessionStart(in copilot.SessionStartHookInput, _ copilot.HookInvocation) (
	out *copilot.SessionStartHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, nil
		}
	}()
	_ = g.keep(Event{Hook: HookSessionStart, Tool: "session", Subject: in.Source}, Verdict{Allowed: true})
	return nil, nil
}

// SessionEnd records how the session ended; whatever was still under way ended with it.
func (g *Gate) SessionEnd(in copilot.SessionEndHookInput, _ copilot.HookInvocation) (
	out *copilot.SessionEndHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, nil
		}
	}()
	g.mu.Lock()
	g.flights = nil
	g.mu.Unlock()
	text := in.FinalMessage
	if in.Error != "" {
		text = in.Error
	}
	_ = g.keep(Event{Hook: HookSessionEnd, Tool: "session", Subject: in.Reason, Detail: clip(text)},
		Verdict{Allowed: true, Reason: in.Reason})
	return nil, nil
}

// ErrorOccurred records an error and aborts: nothing retries where nobody can see it.
func (g *Gate) ErrorOccurred(in copilot.ErrorOccurredHookInput, _ copilot.HookInvocation) (
	out *copilot.ErrorOccurredHookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = &copilot.ErrorOccurredHookOutput{ErrorHandling: "abort"}, nil
		}
	}()
	_ = g.keep(Event{Hook: HookError, Tool: in.ErrorContext, Detail: clip(in.Error)},
		Verdict{Allowed: true, Reason: in.Error})
	return &copilot.ErrorOccurredHookOutput{ErrorHandling: "abort"}, nil
}

// Observe records the session events no hook sees: an MCP server's state, and what the agent said.
func (g *Gate) Observe(e copilot.SessionEvent) {
	defer func() { _ = recover() }()
	switch d := e.Data.(type) {
	case *copilot.SessionMCPServerStatusChangedData:
		var why string
		if d.Error != nil {
			why = *d.Error
		}
		_ = g.keep(Event{Hook: HookServer, Tool: d.ServerName, Subject: string(d.Status),
			Decision: DecisionNoted, Detail: clip(why)}, Verdict{})
	case *copilot.AssistantMessageData:
		if strings.TrimSpace(d.Content) != "" {
			_ = g.keep(Event{Hook: HookMessage, Tool: "agent", Decision: DecisionNoted,
				Detail: clip(d.Content)}, Verdict{})
		}
	}
}

// land records a call's outcome, with how long it ran if the gate saw it approved.
func (g *Gate) land(hook, tool string, args any, outcome, text string) {
	b := g.Bounds()
	e := Event{Hook: hook, Tool: tool, Subject: b.subject(tool, args), Decision: outcome, Detail: clip(text)}
	g.mu.Lock()
	i := slices.IndexFunc(g.flights, func(f Flight) bool { return f.Tool == tool && f.Subject == e.Subject })
	if i < 0 {
		i = slices.IndexFunc(g.flights, func(f Flight) bool { return f.Tool == tool })
	}
	if i >= 0 {
		f := g.flights[i]
		g.flights = slices.Delete(g.flights, i, i+1)
		e.Took, e.Step = g.Now().Sub(f.Since), f.Step
	}
	g.mu.Unlock()
	if i < 0 {
		e.Step = decide(func() Verdict { return g.call(b, tool, args) }).Step
	}
	_ = g.keep(e, Verdict{})
}

// keep stamps and records an event, taking its decision from v unless it already has an outcome.
func (g *Gate) keep(e Event, v Verdict) error {
	if e.At.IsZero() {
		e.At = g.Now()
	}
	if e.Decision == "" {
		e.Decision, e.Step, e.Reason = decisionOf(v), v.Step, v.Reason
		if e.Hook == HookSessionStart || e.Hook == HookSessionEnd || e.Hook == HookError {
			e.Decision = DecisionNoted
		}
	}
	return g.record(e)
}

func decisionOf(v Verdict) string {
	switch {
	case v.Review:
		return DecisionReview
	case v.Allowed:
		return DecisionAllowed
	}
	return DecisionRefused
}

func decide(f func() Verdict) (v Verdict) {
	defer func() {
		if r := recover(); r != nil {
			v = refuse("the gate failed deciding this call, so it is refused: %v", r)
		}
	}()
	return f()
}

func unrecorded(err error) string {
	return fmt.Sprintf("the timeline could not record this call, and a call nobody can audit does not run: %v", err)
}

func deny(why string) *copilot.PreToolUseHookOutput {
	return &copilot.PreToolUseHookOutput{PermissionDecision: "deny", PermissionDecisionReason: why}
}

func reject(why string) rpc.PermissionDecision { return &rpc.PermissionDecisionReject{Feedback: &why} }

// describe names a permission request for the timeline: the tool or kind, what it touches, and
// the arguments it carries.
func (b Bounds) describe(req rpc.PermissionRequest) (string, string, any) {
	switch r := req.(type) {
	case nil:
		return "unknown", "", nil
	case *rpc.PermissionRequestRead:
		return "read", b.name(r.Path), nil
	case *rpc.PermissionRequestWrite:
		return "write", b.name(r.FileName), nil
	case *rpc.PermissionRequestMCP:
		return r.ToolName, b.subject(r.ToolName, r.Args), r.Args
	case *rpc.PermissionRequestHook:
		return r.ToolName, b.subject(r.ToolName, r.ToolArgs), r.ToolArgs
	case *rpc.PermissionRequestCustomTool:
		return r.ToolName, "", r.Args
	case *rpc.PermissionRequestShell:
		return "shell", clip(r.FullCommandText), nil
	}
	return string(req.Kind()), "", nil
}

// subject names what a call is about, as the pack names it.
func (b Bounds) subject(tool string, args any) string {
	fields, _ := args.(map[string]any)
	pick := func(keys ...string) string {
		for _, k := range keys {
			switch v := fields[k].(type) {
			case string:
				return b.name(v)
			case []any:
				parts := make([]string, 0, len(v))
				for _, item := range v {
					if s, ok := item.(string); ok {
						parts = append(parts, b.name(s))
					}
				}
				return strings.Join(parts, ", ")
			}
		}
		return ""
	}
	switch {
	case tool == "view" || tool == "create" || tool == "edit":
		return pick("path")
	case tool == "glob" || tool == "grep":
		pattern, _ := fields["pattern"].(string)
		return strings.TrimSpace(pick("paths") + " " + pattern)
	case tool == "bash":
		command, _ := fields["command"].(string)
		return clip(command)
	case strings.HasPrefix(tool, Server+"-"):
		if name, ok := fields["queryName"].(string); ok {
			return name + " on " + pick("database")
		}
		return pick("tests", "query", "database", "basePath", "workspace_uri")
	}
	return ""
}

// name is a path as the pack names it, or resolved in full when it lies outside, so an attempt
// on another directory is recorded as where it actually pointed.
func (b Bounds) name(p string) string {
	real, err := b.resolve(p)
	if err != nil {
		return clip(p)
	}
	return b.rel(real)
}

// textArgs carry the agent's working text, which the timeline replaces with its size.
var textArgs = map[string]bool{"file_text": true, "old_str": true, "new_str": true, "ql_code": true}

func detail(args any) string {
	if args == nil {
		return ""
	}
	fields, ok := args.(map[string]any)
	if !ok {
		return clip(fmt.Sprint(args))
	}
	shown := make(map[string]any, len(fields))
	for k, v := range fields {
		if s, ok := v.(string); ok && textArgs[k] {
			shown[k] = fmt.Sprintf("<%d characters>", len(s))
			continue
		}
		shown[k] = v
	}
	body, err := json.Marshal(shown)
	if err != nil {
		return ""
	}
	return clip(string(body))
}

// resultText is what a tool reported back to the agent.
func resultText(result any) string {
	switch r := result.(type) {
	case map[string]any:
		if s, ok := r["textResultForLlm"].(string); ok {
			return s
		}
	case copilot.ToolResult:
		return r.TextResultForLLM
	case string:
		return r
	}
	return ""
}

// clip keeps the first maxDetail bytes, cut on a character boundary.
func clip(s string) string {
	if len(s) <= maxDetail {
		return s
	}
	const mark = "…"
	cut := maxDetail - len(mark)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}
