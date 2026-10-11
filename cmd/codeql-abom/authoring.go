package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

// authoringOptions configure the agent sessions a query request can start.
type authoringOptions struct {
	copilotCLI string
	model      string
	qlmcp      string
	limit      time.Duration
	credits    float64
}

func (o *authoringOptions) register(fs *flag.FlagSet) {
	// Never looked up on PATH: on a workstation running VS Code the first `copilot` there is an
	// editor's shim, which answers that it cannot find the CLI and then waits for input.
	fs.StringVar(&o.copilotCLI, "copilot-cli", os.Getenv("COPILOT_CLI_PATH"),
		"Copilot CLI that runs authoring sessions (default: $COPILOT_CLI_PATH; off when empty)")
	fs.StringVar(&o.model, "authoring-model", harness.DefaultModel, "model authoring sessions run")
	fs.StringVar(&o.qlmcp, "ql-mcp", harness.DefaultQLMCP,
		"command line that starts ql-mcp for authoring sessions")
	fs.DurationVar(&o.limit, "authoring-limit", harness.DefaultLimit, "longest one authoring session may run")
	fs.Float64Var(&o.credits, "authoring-credits", 0,
		"AI credits one authoring session may spend (0: no limit)")
}

// newAuthoring runs sessions over the operator's pack, or nothing when no pack is configured, and
// says how to build the agent for a Copilot CLI named later.
//
// Handed the runner the crawl holds, so a session takes the same lock a crawl and a run take.
func newAuthoring(s store.Backend, o authoringOptions, sup *supervisor.Supervisor, stderr io.Writer,
) (*harness.Sessions, func(cli string) harness.Agent) {
	dir := runner.CustomPackRoot()
	if dir == "" {
		return nil, nil
	}
	lock, codeql := runner.SharedLock(), ""
	tests := &runner.Runner{Lock: lock}
	if r, ok := sup.Expander.Resolver.(*runner.Runner); ok {
		lock, codeql, tests = r.Lock, r.CodeQLPath, r
	}
	sessions := &harness.Sessions{
		Store:     s,
		Workspace: dir,
		Libraries: codeqlLibraries(),
		WorkDir:   filepath.Join(sup.Options.WorkDir, "authoring"),
		Model:     o.model,
		Lock:      lock,
		Limit:     o.limit,
		Version:   tests.Version,
		RunTests:  tests.RunTestsHeld,
		Prepare:   tests.InstallPackHeld,
	}
	agentFor := func(cli string) harness.Agent {
		if info, err := os.Stat(cli); cli == "" || err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return nil
		}
		return &harness.Copilot{
			CLI:     cli,
			Model:   o.model,
			Token:   func(ctx context.Context) (string, error) { return ghauth.Token(ctx, ghauth.Exec) },
			QLMCP:   strings.Fields(o.qlmcp),
			CodeQL:  codeql,
			Packs:   runner.PackSearchPath(),
			Credits: o.credits,
		}
	}
	sessions.Agent = agentFor(o.copilotCLI)
	switch {
	case o.copilotCLI == "":
		fmt.Fprintln(stderr, "serve: no Copilot CLI named, so requests cannot start authoring "+
			"sessions; name one in Settings, with --copilot-cli or COPILOT_CLI_PATH")
	case sessions.Agent == nil:
		fmt.Fprintf(stderr, "serve: %s is not a Copilot CLI this server can run, so requests cannot "+
			"start authoring sessions\n", o.copilotCLI)
	}
	return sessions, agentFor
}

// newToolchain reports what sessions need, putting a CLI path saved in Settings ahead of the flag
// or environment that named the default, so the crawl and sessions start on the saved one.
func newToolchain(s store.Backend, cfg *serveConfig, stderr io.Writer) *toolchain.Toolchain {
	codeql := toolchain.Tool{Path: cfg.discovery.codeqlPath}
	if codeql.Path != "" {
		codeql.Source = "--codeql"
	}
	copilot := toolchain.Tool{Path: cfg.authoring.copilotCLI}
	switch {
	case copilot.Path == "":
	case copilot.Path == os.Getenv("COPILOT_CLI_PATH"):
		copilot.Source = "COPILOT_CLI_PATH"
	default:
		copilot.Source = "--copilot-cli"
	}
	chain := &toolchain.Toolchain{
		Store:   s,
		Pack:    cfg.discovery.customQLDir,
		CodeQL:  codeql,
		Copilot: copilot,
		QLMCP:   strings.Fields(cfg.authoring.qlmcp),
		Token: func(ctx context.Context) error {
			_, err := ghauth.Token(ctx, ghauth.Exec)
			return err
		},
	}
	if err := chain.Load(); err != nil {
		fmt.Fprintf(stderr, "serve: reading the tool paths saved in Settings: %v\n", err)
	}
	codeql, copilot = chain.InUse()
	cfg.discovery.codeqlPath, cfg.authoring.copilotCLI = codeql.Path, copilot.Path
	return chain
}

// codeqlLibraries is where the CLI keeps downloaded packs, whose sources a query is written against.
func codeqlLibraries() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".codeql", "packages")}
}
