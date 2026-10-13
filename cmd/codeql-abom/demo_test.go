package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/demo"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

func demoWorkspace(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "demo")
	var out bytes.Buffer
	if err := run(t.Context(), []string{"demo", "init", "--dir", root,
		"--sources", filepath.Join("..", "..", "testdata", "corpus")}, &out, &out); err != nil {
		t.Fatalf("demo init: %v\n%s", err, out.String())
	}
	return root
}

// An ordinary server over practice data would treat synthetic ownership as an operator's estate.
func TestOrdinaryServeRefusesADemoDatabase(t *testing.T) {
	root := demoWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := runServe(ctx, []string{"--db", demo.Layout{Root: root}.Database(), "--addr", "127.0.0.1:0"}, &out, &out)
	if !errors.Is(err, store.ErrDemoDatabase) {
		t.Fatalf("serve over a demo database: %v", err)
	}
}

// Demo takes its paths from the workspace alone; a production flag is a mistake to refuse, not
// a path to follow.
func TestDemoServeRefusesWhatOnlyAnOrdinaryServerTakes(t *testing.T) {
	root := demoWorkspace(t)
	for _, args := range [][]string{
		{"--dir", root, "--db", filepath.Join(t.TempDir(), "abom.db")},
		{"--dir", root, "--expand"},
		{"--dir", root, "--api-url", "https://api.github.com"},
		{"--dir", root, "--addr", "0.0.0.0:8099"},
		{"--dir", root, "--ql-mcp", "npx -y codeql-development-mcp-server"},
		{"--dir", root, "extra"},
	} {
		var out bytes.Buffer
		if _, err := parseDemoServeArgs(args, &out); err == nil {
			t.Errorf("demo serve accepted %q", args)
		}
	}
}

// A Demo instance is composed from local backends; a production reader built and then disabled
// is one mistake away from being used.
func TestDemoServerIsComposedOnlyFromLocalBackends(t *testing.T) {
	root := demoWorkspace(t)
	ws, err := demo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	cfg, err := parseDemoServeArgs([]string{"--dir", root}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := newDemoServer(cfg, ws, "http://127.0.0.1:1", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Instance.Mode != serve.ModeDemo || srv.Instance.Dataset != ws.Manifest.Dataset ||
		srv.Instance.Repositories != "simulated" {
		t.Errorf("instance %+v", srv.Instance)
	}
	for name, absent := range map[string]bool{
		"Discovery":          srv.Discovery == nil,
		"Enumerate":          srv.Enumerate == nil,
		"EnumerateSetup":     srv.EnumerateSetup == nil,
		"CodeScanningWrites": srv.CodeScanningWrites == nil,
		"FindDatabase":       srv.FindDatabase == nil,
		"Files":              srv.Files == nil,
		"Comparer":           srv.Comparer == nil,
		"VerifyLock":         srv.VerifyLock == nil,
		"SnapshotSubmit":     srv.SnapshotSubmit == nil,
		"Events":             srv.Events == nil,
		"QueueOwned":         srv.QueueOwned == nil,
	} {
		if !absent {
			t.Errorf("a Demo server was given %s", name)
		}
	}
	// A run reaches only what the simulated GitHub holds; an upstream Action is named, never fetched.
	if srv.Served == nil || !srv.Served("acme-corp/payments") || srv.Served("actions/checkout") {
		t.Error("a Demo run is not bounded to the dataset's repositories")
	}
	// Nil here would fall back to the gh CLI and the operator's own credential.
	if srv.Verify == nil || srv.ListRepositories == nil || srv.ReadProperties == nil {
		t.Fatal("a Demo server leaves an identity read to fall back to gh")
	}
	if _, err := srv.Verify(t.Context()); err == nil {
		t.Error("a Demo server verified an identity")
	}
	executor, ok := srv.Executor.(*expand.Executor)
	if !ok {
		t.Fatalf("executor %T", srv.Executor)
	}
	// Installing a pack reaches a registry even when every dependency is cached.
	if r, ok := executor.Resolver.(*runner.Runner); !ok || !r.Offline {
		t.Fatalf("runs evaluate with %#v, want an offline runner", executor.Resolver)
	}
	checkout, ok := executor.Crawl.Fetcher.(*expand.Checkout)
	if !ok || checkout.BaseURL != "http://127.0.0.1:1/git" || checkout.Tokens != nil {
		t.Fatalf("runs fetch through %#v", executor.Crawl.Fetcher)
	}
}

// No credential the container holds reaches Demo or anything it starts.
func TestDemoConfinesItsEnvironment(t *testing.T) {
	env := map[string]string{
		"PATH": "/usr/bin", "GITHUB_TOKEN": "ghu_x", "GH_TOKEN": "gho_x", "COPILOT_GITHUB_TOKEN": "x",
		"GH_ENTERPRISE_TOKEN": "x", "GITHUB_CODESPACE_TOKEN": "x", "MY_SECRET": "x", "HTTPS_PROXY": "http://elsewhere",
	}
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	if err := confineEnvironment(environ, "/tmp/demo/home",
		func(k, v string) error { env[k] = v; return nil },
		func(k string) error { delete(env, k); return nil }); err != nil {
		t.Fatal(err)
	}
	for k := range env {
		if strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "PROXY") {
			t.Errorf("%s survived", k)
		}
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "GH_CONFIG_DIR": "/tmp/demo/home/gh",
	} {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if env["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
}

// ABOM's own HTTP clients reach loopback only, whatever a reader built by mistake asks for.
func TestDemoTransportReachesOnlyLoopback(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer local.Close()
	client := &http.Client{Transport: loopbackOnly(http.DefaultTransport)}
	if resp, err := client.Get(local.URL); err != nil {
		t.Fatalf("loopback: %v", err)
	} else {
		resp.Body.Close()
	}
	for _, target := range []string{"https://api.github.com/user", "https://github.com/", "http://192.0.2.1/"} {
		if resp, err := client.Get(target); err == nil {
			resp.Body.Close()
			t.Errorf("%s was reached", target)
		} else if !strings.Contains(err.Error(), "Demo") {
			t.Errorf("%s: %v, want the Demo refusal", target, err)
		}
	}
}

// Copilot is the one real service a Demo may reach, and only through the egress proxy that bounds
// where a session connects; without one, authoring is unavailable rather than unbounded.
func TestDemoAuthoringNeedsTheEgressProxy(t *testing.T) {
	root := demoWorkspace(t)
	ws, err := demo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	cli := filepath.Join(t.TempDir(), "copilot")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\necho 1.0.88\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		egress string
		want   string
	}{{"", "unavailable"}, {"http://127.0.0.1:8098", "copilot"}} {
		cfg, err := parseDemoServeArgs([]string{"--dir", root, "--copilot-cli", cli,
			"--ql-mcp", "codeql-development-mcp-server", "--egress-proxy", c.egress}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		srv, err := newDemoServer(cfg, ws, "http://127.0.0.1:1", &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if srv.Instance.Authoring != c.want {
			t.Errorf("egress %q: authoring %q (%s), want %q", c.egress, srv.Instance.Authoring,
				srv.Instance.AuthoringReason, c.want)
		}
		if c.want == "unavailable" && (srv.Authoring != nil || !strings.Contains(srv.Instance.AuthoringReason, "egress")) {
			t.Errorf("without egress: sessions %v, reason %q", srv.Authoring != nil, srv.Instance.AuthoringReason)
		}
	}
}

// A child trusts the egress authority only when it is told to, and only alongside the proxy that
// presents it; neither alone is a configuration a Demo accepts.
func TestDemoChildrenTrustTheEgressAuthorityOnlyThroughTheProxy(t *testing.T) {
	root := demoWorkspace(t)
	ca := filepath.Join(t.TempDir(), "egress-ca.pem")
	authority, err := demo.NewAuthority([]string{"api.github.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ca, authority.CertPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDemoServeArgs([]string{"--dir", root, "--egress-ca", ca}, &bytes.Buffer{}); err == nil {
		t.Error("an egress authority was accepted without the proxy that presents it")
	}
	notPEM := filepath.Join(t.TempDir(), "x.pem")
	_ = os.WriteFile(notPEM, []byte("nothing"), 0o644)
	if _, err := parseDemoServeArgs([]string{"--dir", root, "--egress-proxy", "http://127.0.0.1:8091",
		"--egress-ca", notPEM}, &bytes.Buffer{}); err == nil {
		t.Error("a file holding no certificate was accepted as the egress authority")
	}
	env := map[string]string{}
	if err := proxyChildren("http://127.0.0.1:8091", ca, func(k, v string) error { env[k] = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if env["NODE_EXTRA_CA_CERTS"] != ca || env["HTTPS_PROXY"] != "http://127.0.0.1:8091" {
		t.Errorf("children were given %v", env)
	}
}

// The readiness panel tells a Demo user which script supplies Copilot's credential; a script that
// moved leaves them at a dead end the panel still points to.
func TestDemoCredentialRemedyNamesScriptsThatExist(t *testing.T) {
	root := demoWorkspace(t)
	ws, err := demo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	cfg, err := parseDemoServeArgs([]string{"--dir", root}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := newDemoServer(cfg, ws, "http://127.0.0.1:1", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	chain, ok := srv.Toolchain.(*toolchain.Toolchain)
	if !ok {
		t.Fatalf("toolchain %T", srv.Toolchain)
	}
	remedy := chain.Credential.Remedy
	scripts := regexp.MustCompile(`\.devcontainer/[\w./-]+\.sh`).FindAllString(remedy, -1)
	if len(scripts) == 0 {
		t.Fatalf("the remedy names no Codespace script: %q", remedy)
	}
	for _, script := range scripts {
		if _, err := os.Stat(filepath.Join("..", "..", script)); err != nil {
			t.Errorf("the remedy names %s, which does not exist", script)
		}
	}
}
