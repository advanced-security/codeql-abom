package harness

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func aCopilot() *Copilot {
	return &Copilot{
		CLI:     "/opt/copilot/bin/copilot",
		Model:   "claude-opus-5",
		QLMCP:   []string{"/opt/ql-mcp/bin/codeql-development-mcp-server", "--quiet"},
		CodeQL:  "/opt/codeql/codeql",
		Packs:   []string{"/repo/ql", "/repo/.abom/ql/custom"},
		Credits: 25,
	}
}

func aSetup(t *testing.T) Setup {
	t.Helper()
	g, _, p := newGate(t)
	return Setup{
		Home:      filepath.Join(p.root, "session-1", "copilot"),
		Scratch:   filepath.Join(p.root, "session-1", "ql-mcp"),
		Workspace: p.ws,
		Prompt:    "work the request",
		Rules:     "the rules",
		Gate:      g,
		Tools: []copilot.Tool{{Name: ToolCatalog, SkipPermission: true},
			{Name: ToolTarget, SkipPermission: true}},
	}
}

// The CLI offers every built-in tool unless told otherwise, the shell among them, and loads its own
// GitHub MCP server into every session: an allow list is the only list that cannot miss one.
func TestASessionIsOfferedOnlyTheLoopsTools(t *testing.T) {
	cfg := aCopilot().config(aSetup(t))

	want := []string{"builtin:create", "builtin:edit", "builtin:glob", "builtin:grep", "builtin:view",
		"custom:" + ToolCatalog, "custom:" + ToolTarget}
	for _, name := range loopNames() {
		want = append(want, "mcp:"+Server+"-"+name)
	}
	got := slices.Clone(cfg.AvailableTools)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("offered %v\nwant    %v", got, want)
	}
	if !slices.Contains(cfg.DisabledMCPServers, "github-mcp-server") {
		t.Errorf("the CLI's own GitHub server is not disabled: %v", cfg.DisabledMCPServers)
	}
	if len(cfg.MCPServers) != 1 {
		t.Errorf("servers %v, want ql-mcp alone", cfg.MCPServers)
	}
	for _, name := range loopNames() {
		if slices.Contains([]string{"create_codeql_query", "codeql_pack_install"}, name) {
			t.Errorf("%s is offered", name)
		}
	}
}

// A drafting session reads what ABOM hands it and nothing else, so it is offered no file tool, no
// shell and no MCP server, and keeps the GitHub server off like any other.
func TestASealedSessionIsOfferedABOMsToolsAlone(t *testing.T) {
	setup := aSetup(t)
	setup.Sealed = true
	setup.Tools = []copilot.Tool{{Name: ToolResults, SkipPermission: true}, {Name: ToolDraft, SkipPermission: true}}
	cfg := aCopilot().config(setup)

	got := slices.Clone(cfg.AvailableTools)
	slices.Sort(got)
	if want := []string{"custom:" + ToolDraft, "custom:" + ToolResults}; !slices.Equal(got, want) {
		t.Errorf("offered %v, want %v", got, want)
	}
	if len(cfg.MCPServers) != 0 || !slices.Contains(cfg.DisabledMCPServers, "github-mcp-server") {
		t.Errorf("servers %v, disabled %v", cfg.MCPServers, cfg.DisabledMCPServers)
	}
}

// ql-mcp runs the CLI itself and knows nothing of ABOM's search path, so ABOM provides the
// workspace manifest it needs.
func TestQLMCPResolvesPacksAndWritesScratchWhereABOMSays(t *testing.T) {
	s := aSetup(t)
	cfg := aCopilot().config(s)
	server, ok := cfg.MCPServers[Server].(copilot.MCPStdioServerConfig)
	if !ok {
		t.Fatalf("ql-mcp is not a stdio server: %#v", cfg.MCPServers[Server])
	}
	if server.Command != "/opt/ql-mcp/bin/codeql-development-mcp-server" ||
		!slices.Equal(server.Args, []string{"--quiet"}) {
		t.Errorf("ql-mcp starts as %s %v", server.Command, server.Args)
	}
	if !slices.Equal(server.Tools, loopNames()) {
		t.Errorf("ql-mcp offers %v, want the loop's %v", server.Tools, loopNames())
	}
	for key, want := range map[string]string{
		"CODEQL_ADDITIONAL_PACKS": strings.Join([]string{"/repo/ql", "/repo/.abom/ql/custom"},
			string(filepath.ListSeparator)),
		"CODEQL_PATH":          "/opt/codeql/codeql",
		"CODEQL_MCP_WORKSPACE": s.Workspace,
		"CODEQL_MCP_TMP_DIR":   filepath.Join(s.Scratch, "tmp"),
		"CODEQL_QUERY_LOG_DIR": filepath.Join(s.Scratch, "query-logs"),
	} {
		if got := server.Env[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// Everything the CLI would otherwise read from the machine or keep between sessions is off: the
// agent's instructions are ABOM's, and nothing it learns outlives the session.
func TestASessionReadsNoInstructionsAndKeepsNothing(t *testing.T) {
	s := aSetup(t)
	cfg := aCopilot().config(s)
	off := map[string]*bool{
		"config discovery":   cfg.EnableConfigDiscovery,
		"skills":             cfg.EnableSkills,
		"file hooks":         cfg.EnableFileHooks,
		"the session store":  cfg.EnableSessionStore,
		"host git":           cfg.EnableHostGitOperations,
		"on-demand guidance": cfg.EnableOnDemandInstructionDiscovery,
	}
	for what, v := range off {
		if v == nil || *v {
			t.Errorf("%s is not turned off", what)
		}
	}
	if cfg.SkipCustomInstructions == nil || !*cfg.SkipCustomInstructions {
		t.Error("custom instructions are read")
	}
	if cfg.Memory == nil || cfg.Memory.Enabled {
		t.Error("memory is on")
	}
	if cfg.InfiniteSessions == nil || cfg.InfiniteSessions.Enabled == nil || *cfg.InfiniteSessions.Enabled {
		t.Error("infinite sessions are on")
	}
	if cfg.WorkingDirectory != s.Workspace || cfg.Model != "claude-opus-5" {
		t.Errorf("works in %s with %s", cfg.WorkingDirectory, cfg.Model)
	}
	if cfg.SystemMessage == nil || cfg.SystemMessage.Mode != "append" || cfg.SystemMessage.Content != "the rules" {
		t.Errorf("system message %+v, want the rules appended to the SDK's own", cfg.SystemMessage)
	}
	if cfg.OnPermissionRequest == nil || cfg.Hooks == nil || cfg.Hooks.OnPreToolUse == nil {
		t.Error("the gate is not wired")
	}
	if cfg.SessionLimits == nil || cfg.SessionLimits.MaxAiCredits == nil || *cfg.SessionLimits.MaxAiCredits != 25 {
		t.Errorf("credits %+v, want the configured 25", cfg.SessionLimits)
	}
}

func TestNoCreditLimitIsSetWhereNoneWasConfigured(t *testing.T) {
	c := aCopilot()
	c.Credits = 0
	if cfg := c.config(aSetup(t)); cfg.SessionLimits != nil {
		t.Errorf("limits %+v", cfg.SessionLimits)
	}
}

// The runtime holds the model call that would spend past the limit until it is told whether to
// spend more. The limit is the operator's to raise, never the session's, so ABOM always declines.
func TestASessionAtItsCreditLimitDeclinesToSpendMoreAndRemembersWhatItSpent(t *testing.T) {
	answered := make(chan rpc.UIHandlePendingSessionLimitsExhaustedRequest, 2)
	limit := &creditLimit{answer: func(r rpc.UIHandlePendingSessionLimitsExhaustedRequest) { answered <- r }}
	limit.observe(copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: "working"}})
	if err := limit.reached(); err != nil {
		t.Fatalf("a message read as the limit: %v", err)
	}
	limit.observe(copilot.SessionEvent{Data: &copilot.SessionLimitsExhaustedRequestedData{
		RequestID: "r1", MaxAiCredits: 30, UsedAiCredits: 31.810175}})
	select {
	case r := <-answered:
		if r.RequestID != "r1" || r.Response.Action != rpc.UISessionLimitsExhaustedResponseActionCancel {
			t.Errorf("answered %+v, want r1 declined", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held model call was never answered")
	}
	var spent *CreditLimitError
	if err := limit.reached(); !errors.As(err, &spent) || spent.Limit != 30 || spent.Spent != 31.810175 {
		t.Errorf("remembered %v", err)
	}
}

// Copilot CLI may deliver the limit request while the session is still being created. Answering it
// must wait for the session without holding up event delivery, or creating the session could wait
// on an event handler that is itself waiting on the session.
func TestALimitRequestBeforeTheSessionExistsDoesNotHoldUpEventDelivery(t *testing.T) {
	ready := make(chan struct{})
	answered := make(chan struct{})
	limit := &creditLimit{answer: func(rpc.UIHandlePendingSessionLimitsExhaustedRequest) {
		<-ready
		close(answered)
	}}
	delivered := make(chan struct{})
	go func() {
		limit.observe(copilot.SessionEvent{Data: &copilot.SessionLimitsExhaustedRequestedData{
			RequestID: "r1", MaxAiCredits: 30, UsedAiCredits: 31}})
		close(delivered)
	}()
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("event delivery waited on a session that did not yet exist")
	}
	if limit.reached() == nil {
		t.Error("the limit was not remembered before the session existed")
	}
	close(ready)
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request was never answered once the session existed")
	}
}

// Copilot CLI 1.0.88 asks nothing of a session like this one: it blocks the model call, says so in
// a limits status the SDK does not type, and fails the turn. The payload is the one it emitted.
func TestASessionTheRuntimeBlockedAtItsCreditLimitRemembersWhatItSpent(t *testing.T) {
	limit := &creditLimit{answer: func(r rpc.UIHandlePendingSessionLimitsExhaustedRequest) {
		t.Errorf("answered %+v, which nothing asked", r)
	}}
	limit.observe(copilot.SessionEvent{Data: &copilot.RawSessionEventData{
		EventType: "model.response_limits_status",
		Raw: json.RawMessage(`{"kind":"response_limits_status","turn":0.0,"status":{"aiCreditsUsed":33.07655,` +
			`"aiCreditsRemaining":0.0,"maxAiCredits":30.0,"isLimitsExhausted":false,"isFinalModelCall":true},` +
			`"state":"final","message":"This is the session's last model call."}`),
	}})
	if err := limit.reached(); err != nil {
		t.Fatalf("a status that is not exhausted read as the limit: %v", err)
	}
	limit.observe(copilot.SessionEvent{Data: &copilot.RawSessionEventData{
		EventType: "model.response_limits_status",
		Raw: json.RawMessage(`{"kind":"response_limits_status","turn":0.0,"status":{"aiCreditsUsed":33.07655,` +
			`"aiCreditsRemaining":0.0,"maxAiCredits":30.0,"isLimitsExhausted":true,"isFinalModelCall":false},` +
			`"state":"blocked","message":"Session limit reached. Increase or unset the session limit to continue."}`),
	}})
	var spent *CreditLimitError
	if err := limit.reached(); !errors.As(err, &spent) || spent.Limit != 30 || spent.Spent != 33.07655 {
		t.Errorf("remembered %v", err)
	}
}
