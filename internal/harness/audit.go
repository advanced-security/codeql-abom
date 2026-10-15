package harness

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/audit"
	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/store"
	copilot "github.com/github/copilot-sdk/go"
)

var hookNames = []string{HookPreToolUse, HookPermission, HookPostToolUse, HookPostToolUseFailure,
	HookSessionStart, HookSessionEnd, HookError, "prompt-submitted", "prompt-transformed", "agent-stop", "pre-mcp-tool-call"}

func (s *Sessions) timeoutSource() string {
	if s.Limit <= 0 {
		return "default"
	}
	return "configured"
}

func (s *Sessions) finishAudit(l *live, state, detail string) {
	e := Event{Source: "abom", Hook: "abom-session-end", Tool: "session", Decision: state, Reason: audit.Text(detail)}
	if err := s.finalInputs(l, &e); err != nil {
		_ = l.gate.fail(err)
		return
	}
	_ = l.gate.keep(e, Verdict{})
}

func (s *Sessions) finalInputs(l *live, e *Event) error {
	b := l.gate.Bounds()
	if !b.bound() {
		return nil
	}
	meta, err := s.Store.AuditSession(l.session)
	if err != nil || meta.RequestID == 0 {
		return err
	}
	id, _, err := s.observedVersion(meta.RequestID)
	if err != nil {
		return err
	}
	q, found, _ := authoring.Find(s.Workspace, id)
	if found {
		e.QueryID, e.QueryVersion = q.ID, q.Version
		e.Capture.Inputs = s.inputDigests(b, q)
	}
	return nil
}

func (s *Sessions) inputDigests(b Bounds, q authoring.Query) map[string]string {
	out := map[string]string{"queryVersion": q.Version, "capturedWhen": "per-file observations after the operation; measured test input digests are recorded separately"}
	paths := []string{"qlpack.yml", "codeql-pack.yml", "codeql-pack.lock.yml"}
	for path := range b.writableFiles() {
		paths = append(paths, path)
	}
	err := filepath.WalkDir(b.Tests(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".testproj") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !slices.Contains(fixtureExts, filepath.Ext(path)) {
			return nil
		}
		if len(paths) >= 48 {
			out["omission"] = "additional fixture paths omitted: bounded manifest"
			return fs.SkipAll
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		out["fixtures"] = "unavailable: fixture tree could not be enumerated"
	}
	slices.Sort(paths)
	for _, path := range paths {
		out[filepath.ToSlash(b.rel(path))] = permittedInputDigest(b, path)
	}
	out["sourceAvailability"] = "digests only; files are not archived and may have changed"
	return out
}

func permittedInputDigest(b Bounds, path string) string {
	real, err := b.resolve(path)
	if err != nil || b.readable(real) != nil || !within(b.Workspace(), real) {
		return "unavailable: outside permitted roots or unresolved path"
	}
	f, err := os.Open(real)
	if err != nil {
		return "unavailable: input not readable"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "unavailable: not a regular input file"
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unavailable: input not readable"
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func (g *Gate) fail(err error) error {
	g.mu.Lock()
	if g.fatal == nil {
		g.fatal = errors.New("audit recording failed; evidence is incomplete and this session cannot continue")
	}
	fatal, cancel := g.fatal, g.cancel
	g.mu.Unlock()
	slog.Error("authoring audit recording failed", "error", audit.Text(err.Error()))
	if cancel != nil {
		cancel()
	}
	return fatal
}

func (g *Gate) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fatal
}

func (g *Gate) PromptSubmitted(in copilot.UserPromptSubmittedHookInput, _ copilot.HookInvocation) (*copilot.UserPromptSubmittedHookOutput, error) {
	err := g.keep(Event{At: in.Timestamp, Hook: "prompt-submitted", Tool: "session", Decision: DecisionNoted,
		Capture: store.AuditCapture{RuntimeSessionID: in.SessionID}, Detail: detail(map[string]any{"prompt": in.Prompt})}, Verdict{})
	return nil, err
}

func (g *Gate) PromptTransformed(in copilot.UserPromptTransformedHookInput, _ copilot.HookInvocation) (*copilot.UserPromptTransformedHookOutput, error) {
	err := g.keep(Event{At: in.Timestamp, Hook: "prompt-transformed", Tool: "session", Decision: DecisionNoted,
		Capture: store.AuditCapture{RuntimeSessionID: in.SessionID}, Detail: detail(map[string]any{"prompt": in.TransformedPrompt})}, Verdict{})
	return nil, err
}

func (g *Gate) AgentStop(in copilot.AgentStopHookInput, _ copilot.HookInvocation) (*copilot.AgentStopHookOutput, error) {
	err := g.keep(Event{At: in.Timestamp, Hook: "agent-stop", Tool: "session", Decision: DecisionNoted,
		Capture: store.AuditCapture{RuntimeSessionID: in.SessionID}, Reason: in.StopReason,
		Detail: detail(map[string]any{"message": "An agent-stop observation is not the end of the ABOM session. Transcript not read."})}, Verdict{})
	return nil, err
}

func (g *Gate) PreMCP(in copilot.PreMCPToolCallHookInput, _ copilot.HookInvocation) (*copilot.PreMCPToolCallHookOutput, error) {
	err := g.keep(Event{At: in.Timestamp, Hook: "pre-mcp-tool-call", Tool: in.ToolName, Decision: DecisionNoted,
		Capture: store.AuditCapture{RuntimeSessionID: in.SessionID, CallID: in.ToolCallID}, Detail: detail(in.Arguments)}, Verdict{})
	return nil, err
}

func (b Bounds) description(sealed bool) audit.Bounds {
	d := audit.Bounds{Writable: map[string]string{}, FixtureExtensions: []string{}, ExcludedNames: []string{},
		ReadOnly: []string{"pack (except the explicitly editable files)"}, Denied: []string{},
		ToolArguments: map[string][]string{}}
	for i := range b.libraries {
		d.ReadOnly = append(d.ReadOnly, fmt.Sprintf("read-root-%d", i+1))
	}
	for i := range b.denied {
		d.Denied = append(d.Denied, fmt.Sprintf("protected-root-%d", i+1))
	}
	if sealed {
		d.QueryHeld = "sealed drafting: no filesystem edits, shell, MCP or CodeQL tools"
		return d
	}
	for name, tool := range loopTools {
		fields := []string{}
		for arg, check := range tool.args {
			fn := runtime.FuncForPC(reflect.ValueOf(check).Pointer()).Name()
			fn = fn[strings.LastIndex(fn, ".")+1:]
			required := "optional"
			if slices.Contains(tool.required, arg) {
				required = "required"
			}
			fields = append(fields, arg+": "+fn+" ("+required+")")
		}
		slices.Sort(fields)
		d.ToolArguments[Server+"-"+name] = fields
	}
	if !b.bound() {
		d.QueryHeld = "target not selected; no files are writable"
		return d
	}
	d.Target, d.FixtureRoot = filepath.ToSlash(b.rel(b.query)), filepath.ToSlash(b.rel(b.tests))
	for path, step := range b.writableFiles() {
		if !b.isDenied(path) {
			d.Writable[filepath.ToSlash(b.rel(path))] = step
		}
	}
	d.FixtureExtensions = slices.Clone(fixtureExts)
	d.ExcludedNames = append(slices.Clone(manifests), ".expected (except target)", ".qlref", ".ql", ".qll", ".actual", "test database", "protected roots", "symlinks outside permitted roots")
	if b.firstChange != nil {
		d.QueryHeld = audit.Text(b.firstChange())
	}
	return d
}

func (g *Gate) startPolicy(ctx context.Context, s Setup, cfg *copilot.SessionConfig, c *Copilot) error {
	p := audit.Policy{Version: audit.Version, Purpose: "authoring", TimeoutMs: s.Timeout.Milliseconds(),
		TimeoutSource: s.TimeoutSource, Runtime: map[string]string{}, Tools: []string{}, Hooks: slices.Clone(hookNames),
		Enforcement: "The session ends at its deadline. Copilot enforces an AI credit cap when one is set. Each CodeQL tool call has its own timeout. Every tool call is still checked against the editable files and its argument rules."}
	if s.Sealed {
		p.Purpose = "sealed drafting"
	}
	if deadline, ok := ctx.Deadline(); ok {
		p.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		p.Runtime["abom"] = info.Main.Version
		for _, dep := range info.Deps {
			if dep.Path == "github.com/github/copilot-sdk/go" {
				p.Runtime["sdk"] = dep.Version
			}
		}
	}
	if cfg != nil {
		p.Tools = slices.Clone(cfg.AvailableTools)
		p.Model = cfg.Model
		p.CreditsKnown = true
		if cfg.SessionLimits != nil {
			p.MaxAICredits = cfg.SessionLimits.MaxAiCredits
		}
		if m, ok := cfg.MCPServers[Server].(copilot.MCPStdioServerConfig); ok {
			p.MCPTimeoutMs = m.Timeout
		}
	}
	if c != nil {
		p.Runtime["ql-mcp configured command"] = audit.Text(strings.Join(c.QLMCP, " "))
	}
	for key, value := range s.Runtime {
		p.Runtime[key] = audit.Text(value)
	}
	g.mu.Lock()
	g.policy = &p
	g.mu.Unlock()
	return g.refreshPolicy()
}

func (g *Gate) refreshPolicy() error {
	g.recordMu.Lock()
	defer g.recordMu.Unlock()
	return g.refreshPolicyLocked()
}

func (g *Gate) refreshPolicyLocked() error {
	g.mu.Lock()
	if g.policy == nil {
		g.mu.Unlock()
		return nil
	}

	p, previous := *g.policy, g.policyJSON
	b := g.bounds
	g.mu.Unlock()
	p.Bounds = b.description(p.Purpose == "sealed drafting")
	body, err := p.JSON()
	if err != nil {
		return g.fail(err)
	}
	if previous == body {
		return nil
	}
	// Persist the whole normative description before allowing the next operation.
	e := Event{At: g.Now(), Source: "abom", Hook: "policy", Tool: "session", Decision: DecisionNoted,
		Capture: store.AuditCapture{Version: audit.Version, Policy: &p, Correlation: "ABOM policy snapshot"}}
	if err := g.record(e); err != nil {
		return g.fail(err)
	}
	g.mu.Lock()
	g.policyJSON = body
	g.mu.Unlock()
	return nil
}

func (g *Gate) guardedTools(tools []copilot.Tool) []copilot.Tool {
	out := slices.Clone(tools)
	for i := range out {
		handle, name := out[i].Handler, out[i].Name
		out[i].Handler = func(in copilot.ToolInvocation) (copilot.ToolResult, error) {
			e := Event{Source: "abom", Hook: "custom-tool-start", Tool: name, Decision: DecisionAllowed,
				Detail: detail(in.Arguments), Capture: store.AuditCapture{RuntimeSessionID: in.SessionID, CallID: in.ToolCallID}}
			if err := g.keep(e, Verdict{}); err != nil {
				return copilot.ToolResult{}, err
			}
			start := g.Now()
			result, err := handle(in)
			e.Hook, e.Decision, e.Detail = "custom-tool-complete", DecisionSucceeded, detail(map[string]any{"resultType": result.ResultType, "omitted": "opaque tool output"})
			if err != nil || result.ResultType == "failure" {
				e.Decision = DecisionFailed
			}
			if in.ToolCallID != "" {
				e.Took = g.Now().Sub(start)
			}
			if recordErr := g.keep(e, Verdict{}); recordErr != nil {
				return copilot.ToolResult{}, recordErr
			}
			return result, err
		}
	}
	return out
}
