package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Setup is everything one session starts with.
type Setup struct {
	Timeout       time.Duration
	TimeoutSource string
	Runtime       map[string]string
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
	// Sealed offers the agent ABOM's tools and nothing else: no files, no shell and no ql-mcp.
	Sealed bool
	// Next is what the agent is told each time it goes idle, empty when nothing is left to tell it.
	Next func(context.Context) string
}

// Agent runs one session to its end and returns what the agent said last.
type Agent interface {
	Run(ctx context.Context, s Setup) (string, error)
}

// builtins are the CLI's own tools the loop needs: reading, searching and writing files. A model has
// one family of them — Claude searches with grep and writes with create and edit, a GPT model with
// rg and apply_patch — and the runtime offers each model only its own, so the loop names both.
var builtins = []string{"view", "glob", "grep", "rg", "create", "edit", "apply_patch"}

// DefaultModel is the model the loop was recorded with.
const DefaultModel = "claude-opus-5"

// DefaultQLMCP starts the ql-mcp release the loop was recorded against.
const DefaultQLMCP = "npx -y codeql-development-mcp-server@2.27.6"

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
	// Mint replaces Token when an App is configured: an installation token minted fresh for each
	// session's Copilot requests, never one inherited from this process's environment.
	Mint func(ctx context.Context, refresh bool) (token string, expires time.Time, err error)
	// QLMCP is the command line that starts ql-mcp.
	QLMCP []string
	// Serial starts the relay that hands ql-mcp one tool call at a time, with QLMCP after it;
	// empty starts ql-mcp directly.
	Serial []string
	// CodeQL is the CLI ql-mcp runs, the one ABOM runs; empty takes codeql from PATH.
	CodeQL string
	// Packs is where ql-mcp resolves packs, the search path ABOM's own invocations use.
	Packs []string
	// Credits bounds what one session may spend, in AI credits; zero sets no bound.
	Credits float64
}

// Run starts the CLI, works the session until the agent goes idle or ctx ends, and stops the CLI.
func (c *Copilot) Run(ctx context.Context, s Setup) (string, error) {
	if c.CLI == "" || len(c.QLMCP) == 0 || (c.Token == nil && c.Mint == nil) {
		return "", ErrNoAgent
	}
	var token string
	if c.Mint != nil {
		// Minted before the CLI starts, so a missing grant refuses the session rather than its first
		// turn, and minted fresh, so the session has the token's whole hour rather than a cached remainder.
		minted, expires, err := c.Mint(ctx, true)
		if err != nil {
			return "", fmt.Errorf("minting the session's Copilot token: %w", err)
		}
		token = minted
		var cancel context.CancelFunc
		ctx, cancel = untilExpiry(ctx, expires)
		defer cancel()
	} else {
		var err error
		if token, err = c.Token(ctx); err != nil {
			return "", fmt.Errorf("reading the GitHub credential: %w", err)
		}
	}
	client := copilot.NewClient(c.clientOptions(s, token))
	if err := client.Start(ctx); err != nil {
		return "", fmt.Errorf("starting the Copilot CLI %s: %w", c.CLI, err)
	}
	defer func() { _ = client.Stop() }()

	if s.Runtime == nil {
		s.Runtime = map[string]string{}
	}
	if status, err := client.GetStatus(ctx); err == nil {
		s.Runtime["copilot-cli"] = status.Version
	}
	cfg := c.config(s)
	var listed []string
	models, listErr := client.ListModels(ctx)
	for _, m := range models {
		listed = append(listed, m.ID)
	}
	model, err := modelFor(cfg.Model, listed, listErr)
	if err != nil {
		return "", err
	}
	if model != cfg.Model {
		_, why := chooseModel(cfg.Model, listed)
		cfg.Model = model
		s.Runtime["model"] = why
		if err := s.Gate.ranOn(model); err != nil {
			return "", fmt.Errorf("recording the session's model: %w", err)
		}
	}
	if err := s.Gate.startPolicy(ctx, s, cfg, c); err != nil {
		return "", err
	}
	var session *copilot.Session
	ready := make(chan struct{})
	limit := &creditLimit{answer: func(r rpc.UIHandlePendingSessionLimitsExhaustedRequest) {
		<-ready
		if session == nil {
			return
		}
		answer, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = session.RPC.UI.HandlePendingSessionLimitsExhausted(answer, &r)
	}}
	cfg.OnEvent = func(e copilot.SessionEvent) { s.Gate.Observe(e); limit.observe(e) }
	session, err = client.CreateSession(ctx, cfg)
	close(ready)
	if err != nil {
		return "", fmt.Errorf("creating the session: %w", err)
	}
	defer func() { _ = session.Disconnect() }()
	var last string
	for send := s.Prompt; send != ""; {
		reply, err := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: send})
		if ctx.Err() != nil {
			// Waiting ends with the context; the agent's turn does not, until it is aborted.
			abort, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = session.Abort(abort)
			if err := s.Gate.Err(); err != nil {
				return "", err
			}
			return "", ctx.Err()
		}
		// Anything more sent past the limit is refused in turn, so the session ends here.
		if spent := limit.reached(); spent != nil {
			return "", spent
		}
		if err != nil {
			return "", err
		}
		if reply != nil {
			if d, ok := reply.Data.(*copilot.AssistantMessageData); ok {
				last = d.Content
			}
		}
		send = ""
		if s.Next != nil {
			send = s.Next(ctx)
		}
	}
	return last, nil
}

// clientOptions starts the CLI with the operator's token, or, with Mint, with the installation token
// minted for the session, no logged-in user and an environment holding no GitHub credential, so the
// minted token is the only one the child can find.
func (c *Copilot) clientOptions(s Setup, token string) *copilot.ClientOptions {
	opts := &copilot.ClientOptions{
		Connection:       copilot.StdioConnection{Path: c.CLI, Env: withoutServerSecrets(os.Environ())},
		Mode:             copilot.ModeEmpty,
		BaseDirectory:    s.Home,
		WorkingDirectory: s.Workspace,
		GitHubToken:      token,
		LogLevel:         "error",
		ClientInfo:       &copilot.ClientInfo{ApplicationName: "codeql-abom"},
	}
	if c.Mint != nil {
		opts.UseLoggedInUser = copilot.Bool(false)
		opts.Connection = copilot.StdioConnection{Path: c.CLI, Env: withoutGitHubTokens(withoutServerSecrets(os.Environ()))}
	}
	return opts
}

// tokenMargin is how long before an installation token ends that a session working on it stops. The
// CLI takes no refresh for a client token, and its token provider refuses an installation token for
// naming no account, so a session lives inside the one hour the token minted for it is good.
const tokenMargin = 5 * time.Minute

// untilExpiry ends ctx a margin before the token the session started on expires.
func untilExpiry(ctx context.Context, expires time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, expires.Add(-tokenMargin))
}

// modelFor is the model a session runs on, from the list the runtime offers its credential. A list
// that could not be read, or offers nothing, refuses the session: running the flag's model anyway is
// exactly a model nobody said it may run.
func modelFor(want string, listed []string, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("reading the models this credential is offered: %w", err)
	}
	if len(listed) == 0 {
		return "", errors.New("the Copilot runtime offers this credential no model")
	}
	model, _ := chooseModel(want, listed)
	return model, nil
}

// autoModel is the runtime's own choice of model, offered to credentials that may use no other.
const autoModel = "auto"

// chooseModel runs the configured model where the runtime lists it, and otherwise its own choice
// rather than refusing to start: an App installation is offered far fewer models than a person. why
// says what was run instead, and is empty where the configured model runs or no list was read.
func chooseModel(want string, listed []string) (model, why string) {
	if slices.Contains(listed, want) {
		return want, ""
	}
	model = listed[0]
	if slices.Contains(listed, autoModel) {
		model = autoModel
	}
	if want == "" {
		return model, "no model is configured, so " + model + " ran"
	}
	return model, want + " is not offered to this credential, so " + model + " ran; offered: " + strings.Join(listed, ", ")
}

// gitHubTokenVariables are where the Copilot CLI, or gh beneath it, would find a credential to use.
var gitHubTokenVariables = []string{
	"COPILOT_GITHUB_TOKEN", "COPILOT_SDK_AUTH_TOKEN", "GH_TOKEN", "GITHUB_TOKEN",
	"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
}

func withoutGitHubTokens(env []string) []string {
	return without(env, func(name string) bool {
		return slices.ContainsFunc(gitHubTokenVariables, func(v string) bool { return strings.EqualFold(v, name) })
	})
}

// serverSecrets are what the server itself signs in with: its database, its identity provider, its
// webhook and the cloud account its object store and decision key live in. The agent reads what a
// repository and a prompt put in front of it, so none of them reaches it.
var serverSecrets = []string{"DATABASE_URL", "ABOM_DB", "ABOM_READ_REPLICA", "ABOM_OIDC_CLIENT_SECRET",
	"ABOM_WEBHOOK_SECRET", "ABOM_AUTHZ_KMS_KEY"}

func withoutServerSecrets(env []string) []string {
	return without(env, func(name string) bool {
		return strings.HasPrefix(strings.ToUpper(name), "AWS_") ||
			slices.ContainsFunc(serverSecrets, func(v string) bool { return strings.EqualFold(v, name) })
	})
}

func without(env []string, drop func(name string) bool) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !drop(name) {
			out = append(out, kv)
		}
	}
	return out
}

// config is the session: the gate on every call, the loop's tools and nothing else, and none of
// what the CLI would otherwise read from the machine or keep between sessions.
func (c *Copilot) config(s Setup) *copilot.SessionConfig {
	tools := copilot.NewToolSet()
	if !s.Sealed {
		tools.AddBuiltIn(builtins...)
		for _, name := range loopNames() {
			tools.AddMCP(Server + "-" + name)
		}
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
		line := append(slices.Clone(c.Serial), c.QLMCP...)
		command, args = line[0], line[1:]
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
		OnEvent:                            s.Gate.Observe,
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
	if s.Sealed {
		cfg.MCPServers = nil
	}
	cfg.Tools = s.Gate.guardedTools(cfg.Tools)
	return cfg
}

// ErrNoAgent is a session asked of a server that was not given a Copilot CLI to run one.
var ErrNoAgent = errors.New("this server was started without a Copilot CLI, so it cannot run an " +
	"authoring session; name one with --copilot-cli or COPILOT_CLI_PATH")

// CreditLimitError is a session its runtime stopped at the credit limit it was started with.
type CreditLimitError struct {
	Limit, Spent float64
}

func (e *CreditLimitError) Error() string {
	return fmt.Sprintf("stopped at the session's limit of %s AI credits, having spent %s",
		credits(e.Limit), credits(e.Spent))
}

func credits(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// creditLimit is what a session learns when its runtime stops it at its credit limit. Copilot CLI
// 1.0.88 blocks the model call and reports it in a limits status; the documented flow instead holds
// the call until it is answered, and the answer is always to spend nothing more: the limit is the
// operator's to raise, never the session's.
type creditLimit struct {
	answer func(rpc.UIHandlePendingSessionLimitsExhaustedRequest)
	mu     sync.Mutex
	spent  *CreditLimitError
}

// limitsStatus is the part of the runtime's limits status a session needs; the SDK does not type it.
type limitsStatus struct {
	Status struct {
		AICreditsUsed     float64 `json:"aiCreditsUsed"`
		MaxAICredits      float64 `json:"maxAiCredits"`
		IsLimitsExhausted bool    `json:"isLimitsExhausted"`
	} `json:"status"`
}

func (c *creditLimit) observe(e copilot.SessionEvent) {
	switch d := e.Data.(type) {
	case *copilot.SessionLimitsExhaustedRequestedData:
		c.remember(d.MaxAiCredits, d.UsedAiCredits)
		go c.answer(rpc.UIHandlePendingSessionLimitsExhaustedRequest{
			RequestID: d.RequestID,
			Response:  rpc.UISessionLimitsExhaustedResponse{Action: rpc.UISessionLimitsExhaustedResponseActionCancel},
		})
	case *copilot.RawSessionEventData:
		var s limitsStatus
		if d.EventType == "model.response_limits_status" && json.Unmarshal(d.Raw, &s) == nil &&
			s.Status.IsLimitsExhausted {
			c.remember(s.Status.MaxAICredits, s.Status.AICreditsUsed)
		}
	}
}

func (c *creditLimit) remember(limit, spent float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spent == nil {
		c.spent = &CreditLimitError{Limit: limit, Spent: spent}
	}
}

func (c *creditLimit) reached() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spent == nil {
		return nil
	}
	return c.spent
}
