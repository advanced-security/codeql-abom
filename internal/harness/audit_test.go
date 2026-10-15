package harness

import (
	"errors"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestAllPinnedHooksAreRegisteredAndPostRecordingFailureIsFatal(t *testing.T) {
	g, j, p := newGate(t)
	h := g.Hooks()
	if h.OnUserPromptSubmitted == nil || h.OnUserPromptTransformed == nil || h.OnAgentStop == nil || h.OnPreMCPToolCall == nil {
		t.Fatal("four pinned callbacks are missing")
	}
	_, _ = h.OnUserPromptSubmitted(copilot.UserPromptSubmittedHookInput{Prompt: "request"}, copilot.HookInvocation{})
	_, _ = h.OnUserPromptTransformed(copilot.UserPromptTransformedHookInput{Prompt: "request"}, copilot.HookInvocation{})
	_, _ = h.OnAgentStop(copilot.AgentStopHookInput{StopReason: "idle"}, copilot.HookInvocation{})
	_, _ = h.OnPreMCPToolCall(copilot.PreMCPToolCallHookInput{ToolName: "codeql_test_run", ToolCallID: "exact"}, copilot.HookInvocation{})
	if len(j.all()) != 4 {
		t.Fatalf("lost callback: %+v", j.all())
	}
	j.fail = errors.New("disk unavailable")
	_, _ = h.OnPostToolUse(copilot.PostToolUseHookInput{ToolName: "view"}, copilot.HookInvocation{})
	j.fail = nil
	answer, _ := h.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "view", ToolArgs: map[string]any{"path": p.ws}}, copilot.HookInvocation{})
	if answer == nil || answer.PermissionDecision != "deny" {
		t.Fatal("recording recovered into unrecorded success")
	}
}

func TestSessionConfigObservesCreationBeforeTheSessionExists(t *testing.T) {
	g, _, p := newGate(t)
	c := &Copilot{Model: "test"}
	cfg := c.config(Setup{Gate: g, Workspace: p.root})
	if cfg.OnEvent == nil {
		t.Fatal("creation-time SDK observations are lost")
	}
}
