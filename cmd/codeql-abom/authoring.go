package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

// authoringOptions configure the agent sessions a query request can start.
type authoringOptions struct {
	copilotCLI string
	model      string
	qlmcp      string
	limit      time.Duration
	credits    float64
	// creditsGiven says --authoring-credits was named, which a cluster's default does not override.
	creditsGiven bool
	// concurrency is how many sessions work at once in a cluster; the rest wait their turn.
	concurrency int
	// token is the credential sessions authenticate with; nil asks gh for the operator's.
	token func(context.Context) (string, error)
	// org is the organization an App's Copilot requests are billed to; empty is the App's owner.
	org string
	// mint and credential are set where an App is configured: sessions then authenticate with an
	// installation token minted for them, and readiness says so.
	mint       func(ctx context.Context, refresh bool) (string, time.Time, error)
	credential toolchain.Credential
	// revoke ends a token mint made, once the session it was minted for is done.
	revoke func(ctx context.Context, token string) error
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
		fmt.Sprintf("AI credits one authoring session may spend, %v or more (0: no limit; in a cluster %v unless given)",
			minimumCredits, clusterCredits))
	fs.IntVar(&o.concurrency, "authoring-concurrency", 1,
		"authoring sessions that work at once in a cluster; the rest wait their turn")
	fs.StringVar(&o.org, "authoring-org", "",
		"organization a GitHub App's Copilot requests are billed to (default: the App's owner)")
}

// minimumCredits is the least the Copilot CLI takes as a session's limit; it refuses a session asking
// for less. clusterCredits bounds a session in a cluster, where nobody watches each one, unless a
// limit is given.
const (
	minimumCredits = 30
	clusterCredits = 500
)

// check refuses what every session would fail on, and settles the credit limit for where sessions run.
func (o *authoringOptions) check(cluster bool) error {
	if cluster && !o.creditsGiven {
		o.credits = clusterCredits
	}
	switch {
	case o.credits < 0:
		return fmt.Errorf("--authoring-credits is a number of AI credits, not %v", o.credits)
	case o.credits > 0 && o.credits < minimumCredits:
		return fmt.Errorf("--authoring-credits %v: the Copilot CLI refuses a session limit under %v AI credits, "+
			"so every session would fail; give %v or more, or 0 for no limit", o.credits, minimumCredits, minimumCredits)
	case o.concurrency < 1:
		return fmt.Errorf("--authoring-concurrency is how many sessions work at once, at least 1, not %d", o.concurrency)
	}
	return nil
}

// sessionMint mints the token a session in a cluster works on, fresh for its whole hour, from the App;
// without one no session can begin, since in a cluster none works on a person's token.
func sessionMint(mint func(ctx context.Context, refresh bool) (string, time.Time, error),
) func(context.Context) (string, time.Time, error) {
	if mint == nil {
		return func(context.Context) (string, time.Time, error) {
			return "", time.Time{}, errors.New("this server has no GitHub App to mint the session's Copilot " +
				"token with, and in a cluster no session works on a person's token")
		}
	}
	return func(ctx context.Context) (string, time.Time, error) { return mint(ctx, true) }
}

// creditLimit says what a session may spend, as the startup line names it.
func creditLimit(credits float64) string {
	if credits == 0 {
		return "no AI credit limit"
	}
	return fmt.Sprintf("%v AI credits", credits)
}

// newAuthoring runs sessions over the operator's pack, or nothing when no pack is configured, and
// says how to build the agent for a Copilot CLI named later.
//
// Handed the runner the crawl holds, so a session takes the same lock a crawl and a run take.
func newAuthoring(s store.Backend, o authoringOptions, x *expand.Expander, workDir string, stderr io.Writer,
) (*harness.Sessions, func(cli string) harness.Agent) {
	dir := runner.CustomPackRoot()
	if dir == "" {
		return nil, nil
	}
	lock, codeql := runner.SharedLock(), ""
	tests := &runner.Runner{Lock: lock}
	if r, ok := x.Resolver.(*runner.Runner); ok {
		lock, codeql, tests = r.Lock, r.CodeQLPath, r
	}
	sessions := &harness.Sessions{
		Store:     s,
		Workspace: dir,
		Libraries: codeqlLibraries(),
		WorkDir:   filepath.Join(workDir, "authoring"),
		Model:     o.model,
		Lock:      lock,
		Limit:     o.limit,
		Version:   tests.Version,
		RunTests:  tests.RunTestsHeld,
		Prepare:   tests.InstallPackHeld,
	}
	token := o.token
	if token == nil && o.mint == nil {
		token = func(ctx context.Context) (string, error) { return ghauth.Token(ctx, ghauth.Exec) }
	}
	agentFor := func(cli string) harness.Agent {
		if info, err := os.Stat(cli); cli == "" || err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return nil
		}
		return &harness.Copilot{
			CLI:     cli,
			Model:   o.model,
			Token:   token,
			Mint:    o.mint,
			QLMCP:   strings.Fields(o.qlmcp),
			Serial:  relay(),
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
	if mint := cfg.authoring.mint; mint != nil {
		chain.Credential = cfg.authoring.credential
		chain.Token = func(ctx context.Context) error {
			_, _, err := mint(ctx, false)
			return err
		}
	}
	said, err := chain.Load()
	if err != nil {
		fmt.Fprintf(stderr, "serve: reading the tool paths saved in Settings: %v\n", err)
	}
	for _, s := range said {
		fmt.Fprintf(stderr, "serve: %s\n", s)
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
