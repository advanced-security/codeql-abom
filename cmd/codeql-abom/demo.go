package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/demo"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// runDemo serves practice data: fictional repositories, real CodeQL, and nothing real to change.
func runDemo(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	usage := func(w io.Writer) {
		fmt.Fprintln(w, "Usage: codeql-abom demo <init|serve|egress> [flags]")
		fmt.Fprintln(w, "  init    create a demo workspace from the fictional corpus")
		fmt.Fprintln(w, "  serve   serve a demo workspace, with its simulated GitHub on loopback")
		fmt.Fprintln(w, "  egress  forward only the hosts Copilot needs, for a Demo's children")
	}
	if len(args) == 0 {
		usage(stderr)
		return errors.New("demo: missing subcommand")
	}
	switch args[0] {
	case "init":
		return runDemoInit(ctx, args[1:], stdout, stderr)
	case "serve":
		return runDemoServe(ctx, args[1:], stdout, stderr)
	case "egress":
		return runDemoEgress(ctx, args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return nil
	}
	usage(stderr)
	return fmt.Errorf("demo: unknown subcommand %q", args[0])
}

func runDemoInit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("demo init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "directory to create the demo workspace in; absent or empty")
	sources := fs.String("sources", filepath.Join("testdata", "corpus"),
		"the fictional corpus: source trees beside their recorded CodeQL output")
	replace := fs.Bool("replace", false, "start again over an existing demo workspace, and only over one")
	locks := fs.String("pack-locks", "",
		"directory holding the query pack's and test pack's lock files, resolved while provisioning")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || fs.NArg() > 0 {
		fs.Usage()
		return errors.New("demo init: --dir is required, and nothing else is")
	}
	if err := demo.Init(ctx, demo.InitOptions{
		Root: *dir, Sources: *sources, Replace: *replace, PackLocks: *locks, Out: io.Discard,
	}); err != nil {
		return fmt.Errorf("demo init: %w", err)
	}
	ws, err := demo.Open(*dir)
	if err != nil {
		return fmt.Errorf("demo init: %w", err)
	}
	defer func() { _ = ws.Close() }()
	fmt.Fprintf(stdout, "demo: %s holds dataset %s, %d fictional repositories\n",
		ws.Layout.Root, ws.Manifest.Dataset, len(ws.Manifest.Repositories))
	return nil
}

type demoServeConfig struct {
	dir       string
	addr      string
	uiDir     string
	codeql    string
	hosts     []string
	tokenFile string
	egress    string
	authoring authoringOptions
}

func parseDemoServeArgs(args []string, stderr io.Writer) (demoServeConfig, error) {
	fs := flag.NewFlagSet("demo serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg demoServeConfig
	fs.StringVar(&cfg.dir, "dir", "", "demo workspace to serve, made by `codeql-abom demo init`")
	fs.StringVar(&cfg.addr, "addr", "127.0.0.1:8099", "loopback address to serve the interface on")
	fs.StringVar(&cfg.uiDir, "ui-dir", "", "directory containing the published interface")
	fs.StringVar(&cfg.codeql, "codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	hosts := fs.String("allow-host", "",
		"comma-separated names admitted beside loopback, exactly, such as a Codespace's forwarded one")
	fs.StringVar(&cfg.tokenFile, "copilot-token-file", "",
		"file holding the Copilot credential sessions use (default: secrets/copilot-token in the workspace)")
	fs.StringVar(&cfg.egress, "egress-proxy", "",
		"proxy every child process reaches the network through, such as `codeql-abom demo egress` (default: none)")
	fs.StringVar(&cfg.authoring.copilotCLI, "copilot-cli", os.Getenv("COPILOT_CLI_PATH"),
		"Copilot CLI that runs authoring sessions (default: $COPILOT_CLI_PATH; off when empty)")
	fs.StringVar(&cfg.authoring.qlmcp, "ql-mcp", "",
		"installed ql-mcp command line for authoring sessions; Demo never downloads one")
	fs.StringVar(&cfg.authoring.model, "authoring-model", harness.DefaultModel, "model authoring sessions run")
	fs.DurationVar(&cfg.authoring.limit, "authoring-limit", harness.DefaultLimit, "longest one authoring session may run")
	fs.Float64Var(&cfg.authoring.credits, "authoring-credits", 0,
		"AI credits one authoring session may spend (0: no limit)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.dir == "" || fs.NArg() > 0 {
		fs.Usage()
		return cfg, errors.New("demo serve: --dir is required, and takes no arguments")
	}
	host, _, err := net.SplitHostPort(cfg.addr)
	if ip := net.ParseIP(host); err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return cfg, fmt.Errorf("demo serve: %q is not a loopback address; a Demo is reached through "+
			"loopback or a private forwarded port", cfg.addr)
	}
	if fields := strings.Fields(cfg.authoring.qlmcp); len(fields) > 0 &&
		(filepath.Base(fields[0]) == "npx" || filepath.Base(fields[0]) == "npm") {
		return cfg, errors.New("demo serve: --ql-mcp must name an installed ql-mcp; " +
			"a Demo does not download one while it runs")
	}
	for _, h := range strings.Split(*hosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			cfg.hosts = append(cfg.hosts, h)
		}
	}
	if cfg.tokenFile == "" {
		cfg.tokenFile = demo.Layout{Root: cfg.dir}.Secrets() + string(filepath.Separator) + "copilot-token"
	}
	return cfg, nil
}

func runDemoServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseDemoServeArgs(args, stderr)
	if err != nil {
		return err
	}
	// Before anything is started: every child inherits this process's environment.
	if err := confineEnvironment(os.Environ(), filepath.Join(cfg.dir, "home"), os.Setenv, os.Unsetenv); err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	if err := proxyChildren(cfg.egress, os.Setenv); err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	http.DefaultTransport = loopbackOnly(http.DefaultTransport)

	graphviz, err := layout.Require(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	ws, err := demo.Open(cfg.dir)
	if err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	defer func() { _ = ws.Close() }()
	if err := ws.Store.Migrate(); err != nil {
		return fmt.Errorf("demo serve: migrating: %w", err)
	}
	provider, err := (&demo.Provider{Layout: ws.Layout, Manifest: ws.Manifest}).Listen(ctx)
	if err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	openCustomQueries(ws.Layout.Pack(), cfg.codeql, stdout, stderr)
	srv, err := newDemoServer(cfg, ws, provider, stderr)
	if err != nil {
		return fmt.Errorf("demo serve: %w", err)
	}
	srv.Graphviz = graphviz
	if cfg.uiDir != "" {
		srv.UI = os.DirFS(cfg.uiDir)
	}
	fmt.Fprintf(stdout, "demo: dataset %s, %d fictional repositories served on loopback only\n",
		ws.Manifest.Dataset, len(ws.Manifest.Repositories))
	if srv.Instance.Authoring != "copilot" {
		fmt.Fprintf(stdout, "demo: authoring sessions are unavailable: %s\n", srv.Instance.AuthoringReason)
	}
	return serveHTTP(ctx, &http.Server{Addr: cfg.addr, Handler: srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second}, cfg.addr, stdout)
}

// errDemoIdentity answers every read of a real GitHub identity in a Demo.
var errDemoIdentity = errors.New("a Demo instance reads no GitHub identity, account or property: " +
	"its ownership is a demo declaration")

// newDemoServer composes a Demo from local backends only: the marked store, the local CodeQL
// runner, runs fetched from the loopback provider, and Copilot sessions over the demo's own pack.
func newDemoServer(cfg demoServeConfig, ws demo.Workspace, provider string, stderr io.Writer) (*serve.Server, error) {
	s, l := ws.Store, ws.Layout
	runner.SetCustomPackRoot(l.Pack())
	r := &runner.Runner{
		CodeQLPath: cfg.codeql, Lock: runner.SharedLock(), Offline: true,
		Server:    &runner.CLIServer{CodeQLPath: cfg.codeql},
		Evaluator: &runner.QueryServer{CodeQLPath: cfg.codeql},
	}
	databases, err := databaseCache(l.Databases(), defaultDatabaseCacheMaxMB)
	if err != nil {
		return nil, err
	}
	w := s.NewWriter(0)
	checkout := &expand.Checkout{BaseURL: provider + "/git"}
	x := &expand.Expander{
		Queue: crawlQueue(s, frontier.DefaultMaxDepth), Store: s, Ingester: ingest.New(s, w), Writer: w,
		Fetcher: checkout, Probe: checkout, Builder: r, CodeQL: r, Resolver: r, Analyzer: r,
		Decode: expand.Decoder(r.Decode), Databases: databases, Install: r.InstallPack,
		Pace: processPools.Pool(work.PoolFetching, work.UnitGit), OnEvent: logDiscoveryFailures(stderr),
	}
	srv := &serve.Server{
		Store: s, Log: stderr, WorkDir: l.Work(), Pools: processPools, AllowedHosts: cfg.hosts,
		Instance: serve.Instance{
			Mode: serve.ModeDemo, Dataset: ws.Manifest.Dataset, Scenario: ws.Manifest.Scenario,
			Repositories: "simulated",
		},
		Verify: func(context.Context) (ghauth.Identity, error) { return ghauth.Identity{}, errDemoIdentity },
		ListRepositories: func(context.Context, string, bool) ([]ghauth.Repository, error) {
			return nil, errDemoIdentity
		},
		ReadProperties: func(context.Context, string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error) {
			return nil, nil, errDemoIdentity
		},
		Executor: newRunExecutor(s, x, l.Work(), stderr),
		Tester:   r, QueryDir: runner.CustomPackRoot(),
		ReadDeclarations: func(ctx context.Context, unread []store.CatalogQuery) (map[string]string, error) {
			return expand.ReadDeclarations(ctx, s, r, unread)
		},
	}
	if srv.QueryDir != "" {
		srv.Printer = r
	}
	token := func(context.Context) (string, error) { return readCopilotToken(cfg.tokenFile) }
	cfg.authoring.token = token
	srv.Toolchain = &toolchain.Toolchain{
		Store: s, Pack: l.Pack(),
		CodeQL:  toolchain.Tool{Path: cfg.codeql, Source: "--codeql"},
		Copilot: toolchain.Tool{Path: cfg.authoring.copilotCLI, Source: "--copilot-cli"},
		QLMCP:   strings.Fields(cfg.authoring.qlmcp),
		Token:   func(ctx context.Context) error { _, err := token(ctx); return err },
		Fixed:   true,
		Credential: toolchain.Credential{Name: "Copilot credential", Source: "Demo setup",
			Remedy: "run .devcontainer/demo/copilot-login.sh in the Codespace terminal; the next session reads it"},
	}
	srv.Instance.Authoring, srv.Instance.AuthoringReason = "unavailable", "no Copilot CLI was named"
	switch {
	case cfg.authoring.copilotCLI == "":
		return srv, nil
	case cfg.authoring.qlmcp == "":
		srv.Instance.AuthoringReason = "no installed ql-mcp was named"
		return srv, nil
	case cfg.egress == "":
		// Copilot is the one real service a Demo reaches, and only where the proxy lets it.
		srv.Instance.AuthoringReason = "no egress proxy was named, so nothing would bound where a session connects"
		return srv, nil
	}
	if sessions, _ := newAuthoring(s, cfg.authoring, x, l.Work(), io.Discard); sessions != nil {
		srv.Authoring = sessions
		if sessions.Agent != nil {
			srv.Instance.Authoring, srv.Instance.AuthoringReason = "copilot", ""
		}
	}
	return srv, nil
}

// readCopilotToken reads the credential a user supplied after creating the Codespace.
func readCopilotToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no Copilot credential has been supplied to this Demo")
	}
	if err != nil {
		return "", err
	}
	token := string(bytes.TrimSpace(b))
	if token == "" {
		return "", errors.New("the Demo's Copilot credential file is empty")
	}
	return token, nil
}

// credentialNames are environment names that carry, or lead to, a credential.
var credentialNames = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY",
	"ASKPASS", "AUTH", "PROXY"}

// confineEnvironment removes every credential and proxy from the environment Demo and its children
// share, and gives git and gh nothing of the machine's configuration to fall back to.
func confineEnvironment(environ []string, home string, set func(k, v string) error, unset func(string) error) error {
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		for _, c := range credentialNames {
			if strings.Contains(upper, c) {
				if err := unset(name); err != nil {
					return err
				}
				break
			}
		}
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_TERMINAL_PROMPT": "0",
		"GH_CONFIG_DIR": filepath.Join(home, "gh"), "GH_PROMPT_DISABLED": "1",
	} {
		if err := set(k, v); err != nil {
			return err
		}
	}
	return nil
}

// proxyChildren sends every child's network traffic through the egress proxy, loopback excepted.
func proxyChildren(proxy string, set func(k, v string) error) error {
	if proxy == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(proxy, "http://"))
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() || !strings.HasPrefix(proxy, "http://") {
		return fmt.Errorf("--egress-proxy %q is not an http:// proxy on loopback", proxy)
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if err := set(k, proxy); err != nil {
			return err
		}
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if err := set(k, "127.0.0.1,localhost,::1"); err != nil {
			return err
		}
	}
	// Node, which the Copilot CLI and ql-mcp run on, reads the proxy variables only when asked to.
	return set("NODE_USE_ENV_PROXY", "1")
}

// loopbackOnly is the transport every HTTP client this process builds by default uses in a Demo.
func loopbackOnly(next http.RoundTripper) http.RoundTripper {
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		ip := net.ParseIP(strings.Trim(r.URL.Hostname(), "[]"))
		if r.URL.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("a Demo instance does not reach %s: its GitHub is simulated on loopback", r.URL.Host)
		}
		return next.RoundTrip(r)
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// runDemoEgress forwards tunnels to Copilot's hosts and refuses, and records, everything else.
func runDemoEgress(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("demo egress", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:8098", "loopback address to listen on")
	allow := fs.String("allow", strings.Join(demo.CopilotHosts, ","),
		"comma-separated hosts tunnels may reach; a leading dot allows a suffix")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("demo egress: takes no arguments")
	}
	var hosts []string
	for _, h := range strings.Split(*allow, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	fmt.Fprintf(stdout, "demo egress: tunnels from %s reach only %s\n", *addr, strings.Join(hosts, ", "))
	return demo.ListenEgress(ctx, *addr, &demo.Egress{Allow: hosts, Log: stdout})
}
