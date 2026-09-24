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
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
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

// newAuthoring runs sessions over the operator's pack, or nothing when no pack is configured.
//
// Handed the runner the crawl holds, so a session takes the same lock a crawl and a run take.
func newAuthoring(s *store.Store, o authoringOptions, sup *supervisor.Supervisor, stderr io.Writer) serve.Authoring {
	dir := runner.CustomPackRoot()
	if dir == "" {
		return nil
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
	}
	switch info, err := os.Stat(o.copilotCLI); {
	case o.copilotCLI == "":
		fmt.Fprintln(stderr, "serve: no Copilot CLI named, so requests cannot start authoring "+
			"sessions; name one with --copilot-cli or COPILOT_CLI_PATH")
	case err != nil || info.IsDir() || info.Mode()&0o111 == 0:
		fmt.Fprintf(stderr, "serve: %s is not a Copilot CLI this server can run, so requests cannot "+
			"start authoring sessions\n", o.copilotCLI)
	default:
		sessions.Agent = &harness.Copilot{
			CLI:     o.copilotCLI,
			Model:   o.model,
			Token:   func(ctx context.Context) (string, error) { return ghauth.Token(ctx, ghauth.Exec) },
			QLMCP:   strings.Fields(o.qlmcp),
			CodeQL:  codeql,
			Packs:   runner.PackSearchPath(),
			Credits: o.credits,
		}
	}
	return sessions
}

// codeqlLibraries is where the CLI keeps downloaded packs, whose sources a query is written against.
func codeqlLibraries() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".codeql", "packages")}
}
