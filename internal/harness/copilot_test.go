package harness

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
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
