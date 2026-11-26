package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// Sign-in is configured from flags, with the secret from a file or the environment and never from
// a flag a process listing would show; a role map names ABOM roles and nothing else.
func TestSignInIsConfiguredWithoutASecretOnTheCommandLine(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := accessFlags{mode: "oidc", issuer: "https://login.example/tenant/v2.0", clientID: "abom",
		secretFile: secret, redirect: "https://abom.example/auth/callback", roleMap: "Readers=viewer, Ops=operator"}
	cfg, err := c.config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientSecret != "from-file" || cfg.RoleMap["Readers"] != authz.RoleViewer || cfg.RoleMap["Ops"] != authz.RoleOperator {
		t.Errorf("config = %+v", cfg)
	}

	c.secretFile = ""
	t.Setenv(oidcSecretEnv, "from-env")
	if cfg, err := c.config(); err != nil || cfg.ClientSecret != "from-env" {
		t.Errorf("config = %+v, %v; want the secret from the environment", cfg, err)
	}

	c.roleMap = "Readers=superuser"
	if _, err := c.config(); err == nil || !strings.Contains(err.Error(), "superuser") {
		t.Errorf("a role map naming no ABOM role was accepted: %v", err)
	}
	c.roleMap, c.redirect = "", ""
	if _, err := c.config(); err == nil {
		t.Error("sign-in without a redirect URL was accepted")
	}
	if cfg, err := (accessFlags{mode: "none"}).config(); err != nil || cfg.Issuer != "" {
		t.Errorf("no sign-in = %+v, %v", cfg, err)
	}
}

// Signing in with GitHub goes through the reading App, with its client secret from a file or the
// environment, and grants nothing without a role map saying which memberships hold which role.
func TestGitHubSignInNeedsTheReadingAppItsSecretAndARoleMap(t *testing.T) {
	c := accessFlags{mode: "github", githubRedirect: "https://abom.example/auth/callback",
		githubRoleMap: "acme/abom-operators=operator, acme=viewer, acme:owners=admin", githubRefresh: 5 * time.Minute}
	if _, err := c.githubConfig(nil); err == nil || !strings.Contains(err.Error(), "app create") {
		t.Errorf("GitHub sign-in without the reading App = %v; want it to say how to create one", err)
	}
	app := &ghapp.App{Credentials: ghapp.Credentials{AppID: 1, ClientID: "Iv23li"}}
	if _, err := c.githubConfig(app); err == nil || !strings.Contains(err.Error(), githubSecretEnv) {
		t.Errorf("GitHub sign-in without a client secret = %v; want it to say where the secret goes", err)
	}

	t.Setenv(githubSecretEnv, "from-env")
	cfg, err := c.githubConfig(app)
	if err != nil {
		t.Fatal(err)
	}
	if app.Credentials.ClientSecret != "from-env" || cfg.Web != "https://github.com" || cfg.Refresh != 5*time.Minute ||
		cfg.RoleMap["acme/abom-operators"] != authz.RoleOperator || cfg.RoleMap["acme:owners"] != authz.RoleAdmin {
		t.Errorf("config = %+v; secret %q", cfg, app.Credentials.ClientSecret)
	}
	c.githubRoleMap = ""
	if _, err := c.githubConfig(app); err == nil {
		t.Error("GitHub sign-in without a role map was accepted, and would grant nobody anything")
	}
}

// pprof serves the heap, which holds the provider's client secret and every live session. With
// sign-in on, the people reaching the server are not the one at this machine, so the server
// refuses to start with diagnostics rather than leave them to the lowest role.
func TestDiagnosticsAreRefusedWhenPeopleSignIn(t *testing.T) {
	args := []string{"--db", "abom.db", "--diagnostics", "--auth=oidc",
		"--oidc-issuer=https://login.example/t/v2.0", "--oidc-client-id=abom",
		"--oidc-redirect-url=https://abom.example/auth/callback"}
	if _, err := parseServeArgs(args, io.Discard); err == nil || !strings.Contains(err.Error(), "--diagnostics") {
		t.Errorf("diagnostics with sign-in started: %v", err)
	}
	if _, err := parseServeArgs([]string{"--db", "abom.db", "--diagnostics"}, io.Discard); err != nil {
		t.Errorf("diagnostics without sign-in refused: %v", err)
	}
}

// The probe listener is off unless named, and takes its address as given.
func TestTheProbeListenerIsOffUnlessNamed(t *testing.T) {
	off, err := parseServeArgs([]string{"--db", "abom.db"}, io.Discard)
	if err != nil || off.probeAddr != "" {
		t.Fatalf("by default probeAddr = %q, %v", off.probeAddr, err)
	}
	on, err := parseServeArgs([]string{"--db", "abom.db", "--probe-addr", "0.0.0.0:9090"}, io.Discard)
	if err != nil || on.probeAddr != "0.0.0.0:9090" {
		t.Fatalf("probeAddr = %q, %v", on.probeAddr, err)
	}
}
