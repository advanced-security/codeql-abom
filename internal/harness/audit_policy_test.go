package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	copilot "github.com/github/copilot-sdk/go"
)

func TestFinalInputsIdentifyFixturesWithoutFollowingAnEscapingLink(t *testing.T) {
	g, _, _ := newGate(t)
	b := g.Bounds()
	if err := os.MkdirAll(b.Tests(), 0755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(b.Tests(), "audit-fixture.yml")
	if err := os.WriteFile(fixture, []byte("on: push\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("private canary"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b.Tests(), "escape.yml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	s := &Sessions{}
	digests := s.inputDigests(b, authoring.Query{Version: "v"})
	if !strings.HasPrefix(digests[b.rel(fixture)], "sha256:") {
		t.Fatal("fixture digest missing")
	}
	if !strings.Contains(digests[b.rel(link)], "unavailable") {
		t.Fatal("escaping fixture not identified as unavailable")
	}
}

func TestPolicyUsesTheExecutionToolsBoundsCreditsAndActualDeadline(t *testing.T) {
	g, j, p := newGate(t)
	c := &Copilot{Model: "test-model", Credits: 12, QLMCP: []string{"npx", "codeql-development-mcp-server@2.27.2"}}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultLimit)
	defer cancel()
	setup := Setup{Gate: g, Workspace: p.ws, Timeout: DefaultLimit, TimeoutSource: "default"}
	cfg := c.config(setup)
	if err := g.startPolicy(ctx, setup, cfg, c); err != nil {
		t.Fatal(err)
	}
	policy := j.all()[0].Capture.Policy
	deadline, _ := ctx.Deadline()
	if policy == nil || policy.Deadline != deadline.UTC().Format(time.RFC3339Nano) || policy.TimeoutMs != DefaultLimit.Milliseconds() ||
		policy.MaxAICredits == nil || *policy.MaxAICredits != 12 || !slices.Equal(policy.Tools, cfg.AvailableTools) {
		t.Fatalf("policy differs from actual execution: %+v", policy)
	}
	b := g.Bounds()
	for path := range b.writableFiles() {
		if _, ok := policy.Bounds.Writable[b.rel(path)]; !ok {
			t.Errorf("missing exact file %s", path)
		}
	}
	if !slices.Equal(policy.Bounds.FixtureExtensions, fixtureExts) {
		t.Fatal("fixture restrictions drifted")
	}
	old, _ := policy.JSON()
	c.Credits = 99
	after, _ := policy.JSON()
	if old != after {
		t.Fatal("later settings repainted the policy")
	}
}

func TestSealedPolicyHasNoFilesystemOrCodeQLLane(t *testing.T) {
	g, j, p := newGate(t)
	c := &Copilot{Model: "draft"}
	setup := Setup{Gate: g, Workspace: p.ws, Sealed: true, Timeout: time.Minute, TimeoutSource: "configured"}
	if err := g.startPolicy(context.Background(), setup, c.config(setup), c); err != nil {
		t.Fatal(err)
	}
	policy := j.all()[0].Capture.Policy
	if policy.Purpose != "sealed drafting" || len(policy.Tools) != 0 || len(policy.Bounds.Writable) != 0 || !policy.CreditsKnown || policy.MaxAICredits != nil {
		t.Fatalf("sealed policy widened execution: %+v", policy)
	}
}

func TestSDKIdentityDoesNotPairConcurrentHooksOrIngestReasoning(t *testing.T) {
	g, j, _ := newGate(t)
	for _, id := range []string{"a", "b"} {
		g.Observe(copilot.SessionEvent{ID: "start-" + id, Data: &copilot.ToolExecutionStartData{ToolName: "view", ToolCallID: id}})
	}
	_, _ = g.PostToolUse(copilot.PostToolUseHookInput{ToolName: "view", ToolResult: "private-source-canary"}, copilot.HookInvocation{})
	if len(g.InFlight()) != 2 {
		t.Fatal("an ambiguous post hook consumed a runtime flight")
	}
	g.Observe(copilot.SessionEvent{ID: "reasoning", Data: &copilot.AssistantReasoningData{Content: "hidden-reasoning-canary"}})
	g.Observe(copilot.SessionEvent{ID: "done-b", Data: &copilot.ToolExecutionCompleteData{ToolCallID: "b", Success: true}})
	g.Observe(copilot.SessionEvent{ID: "done-b", Data: &copilot.ToolExecutionCompleteData{ToolCallID: "b", Success: true}})
	if len(g.InFlight()) != 1 || g.InFlight()[0].CallID != "a" || len(j.all()) != 4 {
		t.Fatalf("wrong pairing or dedup: %+v", j.all())
	}
	for _, e := range j.all() {
		if strings.Contains(e.Detail, "canary") {
			t.Fatal("source or hidden reasoning entered the audit")
		}
	}
}
