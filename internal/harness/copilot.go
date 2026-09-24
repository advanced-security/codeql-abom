package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Setup is everything one session starts with.
type Setup struct {
	// Home is where the CLI keeps the session's state; Scratch is where ql-mcp writes what CodeQL
	// printed. Both are scrubbed when the session ends.
	Home      string
	Scratch   string
	Workspace string
	// Prompt is the request; Rules are appended to the system message, which a request's own
	// text cannot countermand.
	Prompt string
	Rules  string
	Gate   *Gate
	// Tools are ABOM's own: the catalog, and the target decision.
	Tools []copilot.Tool
}

// Agent runs one session to its end and returns what the agent said last.
type Agent interface {
	Run(ctx context.Context, s Setup) (string, error)
}

// builtins are the CLI's own tools the loop needs: reading, searching and writing files.
var builtins = []string{"view", "glob", "grep", "create", "edit"}

// DefaultModel is the model the loop was recorded with.
const DefaultModel = "claude-opus-5"

// DefaultQLMCP starts the ql-mcp release the loop was recorded against.
const DefaultQLMCP = "npx -y codeql-development-mcp-server@2.27.1"

// mcpTimeout bounds one ql-mcp call; extracting and testing run CodeQL.
const mcpTimeout = int((20 * time.Minute) / time.Millisecond)

// Copilot runs sessions through the Copilot CLI with the SDK, one client and one CLI process per
// session: the directory the CLI keeps state in is a client option, and each session's is its own.
type Copilot struct {
	// CLI is the Copilot CLI to start. Never looked up on PATH, where the first `copilot` can be
	// an editor's shim that waits for input.
	CLI   string
	Model string
	// Token is the GitHub credential sessions authenticate with.
	Token func(context.Context) (string, error)
	// QLMCP is the command line that starts ql-mcp.
	QLMCP []string
	// CodeQL is the CLI ql-mcp runs, the one ABOM runs; empty takes codeql from PATH.
	CodeQL string
	// Packs is where ql-mcp resolves packs, the search path ABOM's own invocations use.
	Packs []string
	// Credits bounds what one session may spend, in AI credits; zero sets no bound.
	Credits float64
}

// Run starts the CLI, works the session until the agent goes idle or ctx ends, and stops the CLI.
func (c *Copilot) Run(ctx context.Context, s Setup) (string, error) {
	if c.CLI == "" || len(c.QLMCP) == 0 || c.Token == nil {
		return "", ErrNoAgent
	}
	token, err := c.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("reading the GitHub credential: %w", err)
	}
	client := copilot.NewClient(&copilot.ClientOptions{
		Connection:       copilot.StdioConnection{Path: c.CLI},
		Mode:             copilot.ModeEmpty,
		BaseDirectory:    s.Home,
		WorkingDirectory: s.Workspace,
		GitHubToken:      token,
		LogLevel:         "error",
		ClientInfo:       &copilot.ClientInfo{ApplicationName: "codeql-abom"},
	})
	if err := client.Start(ctx); err != nil {
		return "", fmt.Errorf("starting the Copilot CLI %s: %w", c.CLI, err)
	}
	defer func() { _ = client.Stop() }()

	session, err := client.CreateSession(ctx, c.config(s))
	if err != nil {
		return "", fmt.Errorf("creating the session: %w", err)
	}
	defer func() { _ = session.Disconnect() }()
	defer session.On(s.Gate.Observe)()

	reply, err := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: s.Prompt})
	if ctx.Err() != nil {
		// Waiting ends with the context; the agent's turn does not, until it is aborted.
		abort, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = session.Abort(abort)
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if reply != nil {
		if d, ok := reply.Data.(*copilot.AssistantMessageData); ok {
			return d.Content, nil
		}
	}
	return "", nil
}

// config is the session: the gate on every call, the loop's tools and nothing else, and none of
// what the CLI would otherwise read from the machine or keep between sessions.
func (c *Copilot) config(s Setup) *copilot.SessionConfig {
	tools := copilot.NewToolSet().AddBuiltIn(builtins...)
	for _, name := range loopNames() {
		tools.AddMCP(Server + "-" + name)
	}
	for _, t := range s.Tools {
		tools.AddCustom(t.Name)
	}
	env := map[string]string{
		"CODEQL_MCP_WORKSPACE": s.Workspace,
		"CODEQL_MCP_TMP_DIR":   filepath.Join(s.Scratch, "tmp"),
		"CODEQL_QUERY_LOG_DIR": filepath.Join(s.Scratch, "query-logs"),
		// Named rather than inherited, in case the CLI hands an MCP server only what it is given.
		"PATH": os.Getenv("PATH"),
		"HOME": os.Getenv("HOME"),
	}
	if len(c.Packs) > 0 {
		env["CODEQL_ADDITIONAL_PACKS"] = strings.Join(c.Packs, string(filepath.ListSeparator))
	}
	if c.CodeQL != "" {
		env["CODEQL_PATH"] = c.CodeQL
	}
	var command string
	var args []string
	if len(c.QLMCP) > 0 {
		command, args = c.QLMCP[0], slices.Clone(c.QLMCP[1:])
	}
	cfg := &copilot.SessionConfig{
		ClientName:       "codeql-abom",
		Model:            c.Model,
		WorkingDirectory: s.Workspace,
		SystemMessage:    &copilot.SystemMessageConfig{Mode: "append", Content: s.Rules},
		Tools:            s.Tools,
		AvailableTools:   tools.ToSlice(),
		// The CLI loads its own GitHub server into every session, which would hand the agent
		// write access to GitHub.
		DisabledMCPServers: []string{"github-mcp-server"},
		MCPServers: map[string]copilot.MCPServerConfig{
			Server: copilot.MCPStdioServerConfig{
				Command: command, Args: args, Env: env, Tools: loopNames(),
				Timeout: mcpTimeout, WorkingDirectory: s.Workspace,
			},
		},
		OnPermissionRequest:                s.Gate.Permission,
		Hooks:                              s.Gate.Hooks(),
		EnableConfigDiscovery:              copilot.Bool(false),
		SkipCustomInstructions:             copilot.Bool(true),
		EnableSkills:                       copilot.Bool(false),
		EnableFileHooks:                    copilot.Bool(false),
		EnableSessionStore:                 copilot.Bool(false),
		EnableHostGitOperations:            copilot.Bool(false),
		EnableOnDemandInstructionDiscovery: copilot.Bool(false),
		InfiniteSessions:                   &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		Memory:                             &copilot.MemoryConfiguration{Enabled: false},
	}
	if c.Credits > 0 {
		credits := c.Credits
		cfg.SessionLimits = &rpc.SessionLimitsConfig{MaxAiCredits: &credits}
	}
	return cfg
}

// ErrNoAgent is a session asked of a server that was not given a Copilot CLI to run one.
var ErrNoAgent = errors.New("this server was started without a Copilot CLI, so it cannot run an " +
	"authoring session; name one with --copilot-cli or COPILOT_CLI_PATH")
