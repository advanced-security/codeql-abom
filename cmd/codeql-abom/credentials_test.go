package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

type lendingTokens struct{}

func (lendingTokens) Token(context.Context, string) (string, bool, error) {
	return "lent-designated", true, nil
}

func (lendingTokens) AccountToken(context.Context, string) (string, error) {
	return "", ghapp.ErrNotInstalled
}

func bearerSeen(t *testing.T) (*httptest.Server, func() string) {
	var mu sync.Mutex
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"tree":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
}

func servedStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

func checkoutsOf(t *testing.T, s *store.Store, tokens ghapp.TokenSource) (*expand.Checkout, *expand.Checkout) {
	t.Helper()
	loop, err := newSupervisor(s, discoveryOptions{workDir: t.TempDir()}, tokens, io.Discard)
	if err != nil {
		t.Fatalf("newSupervisor: %v", err)
	}
	fetcher, ok := loop.Expander.Fetcher.(*expand.Checkout)
	if !ok {
		t.Fatalf("the expander fetches with %T", loop.Expander.Fetcher)
	}
	prober, ok := loop.Rechecker.Probe.(*expand.Checkout)
	if !ok {
		t.Fatalf("the recheck probes with %T", loop.Rechecker.Probe)
	}
	return fetcher, prober
}

// With no App beside the database, every reader keeps the operator's token and the receiver its configured secret, as before.
func TestWithoutAnAppEveryReaderKeepsTheOperatorsToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "github-app")
	app, err := readerApp(dir, "", ghapp.DefaultAPIURL)
	if app != nil || err != nil || readerTokens(app) != nil {
		t.Fatalf("readerApp with nothing configured = %v, %v; want no App and no token source", app, err)
	}
	t.Setenv("GITHUB_TOKEN", "person-token")
	srv, seen := bearerSeen(t)
	_, _ = newReader(context.Background(), srv.URL, nil).Tree(context.Background(), "actions/checkout", "abc")
	if seen() != "Bearer person-token" {
		t.Errorf("a reader without an App sent %q, want the operator's token", seen())
	}
	if got, err := eventSecret(nil, dir, []byte("configured")); err != nil || string(got) != "configured" {
		t.Errorf("without an App the receiver's secret = %q, %v; want the one configured", got, err)
	}
	fetcher, prober := checkoutsOf(t, servedStore(t), nil)
	if fetcher.Tokens != nil || prober.Tokens != nil {
		t.Error("git was given a token source with no App configured")
	}
	if lockVerifierWithPolicy(servedStore(t), nil, func(context.Context, string) error { return nil }, srv.URL).Runner.Token != nil {
		t.Error("lock verification was given an installation token with no App configured")
	}
	if _, err := readerApp(dir, "advanced-security", ghapp.DefaultAPIURL); err == nil {
		t.Error("naming an account for public reads with no App configured was accepted")
	}
}

// With an App, every reader goes through its token source, git included, and the receiver verifies with the App's secret.
func TestWithAnAppEveryReaderReadsThroughItsInstallations(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	secret := "whsec-app-0123456789"
	c := ghapp.Conversion{ID: 4242, Slug: "abom-has-ghas", ClientID: "Iv23liWIRE", WebhookSecret: &secret,
		PEM: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
	dir := filepath.Join(t.TempDir(), "github-app")
	if _, err := ghapp.Save(dir, c, "https://smee.io/abc", ghapp.DefaultAPIURL); err != nil {
		t.Fatal(err)
	}

	app, err := readerApp(dir, "advanced-security", ghapp.DefaultAPIURL)
	if err != nil || app == nil || app.Credentials.AppID != 4242 || app.PublicReads != "advanced-security" {
		t.Fatalf("readerApp = %+v, %v; want App 4242 lending advanced-security's installation", app, err)
	}
	t.Setenv("GITHUB_TOKEN", "person-token")
	srv, seen := bearerSeen(t)
	_, _ = newReader(context.Background(), srv.URL, lendingTokens{}).Tree(context.Background(), "actions/checkout", "abc")
	if seen() != "Bearer lent-designated" {
		t.Errorf("a reader with an App sent %q, want the token source's", seen())
	}

	for given, ok := range map[string]bool{"": true, secret: true, "another-secret": false} {
		got, err := eventSecret(app, dir, []byte(given))
		if ok && (err != nil || string(got) != secret) {
			t.Errorf("with %q given, the receiver's secret = %q, %v; want the App's", given, got, err)
		}
		if !ok && err == nil {
			t.Errorf("a secret other than the App's was accepted beside it: %q", got)
		}
	}

	fetcher, prober := checkoutsOf(t, servedStore(t), lendingTokens{})
	if fetcher.Tokens == nil || prober.Tokens == nil {
		t.Error("the expander's clone or the recheck's probe runs without the App's token source")
	}
	verifier := lockVerifierWithPolicy(servedStore(t), lendingTokens{}, func(context.Context, string) error { return nil }, srv.URL)
	if verifier.Runner.Token == nil {
		t.Fatal("lock verification reads GitHub as the person's gh session with an App configured")
	}
	if token, err := verifier.Runner.Token(context.Background(), "has-ghas"); err != nil || token != "lent-designated" {
		t.Errorf("lock verification reads with %q, %v; want the App's token source", token, err)
	}

	if err := os.Chmod(ghapp.KeyFile(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readerApp(dir, "", ghapp.DefaultAPIURL); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a configured App whose key others can read = %v; want the command stopped", err)
	}
}

// The relay hands deliveries to this server's own receiver, and is refused where nothing could verify them.
func TestServeRelaysToItsOwnReceiver(t *testing.T) {
	for addr, want := range map[string]string{
		"localhost:8080": "http://localhost:8080/api/events/github",
		"127.0.0.1:8099": "http://127.0.0.1:8099/api/events/github",
		":8080":          "http://127.0.0.1:8080/api/events/github",
		"0.0.0.0:9":      "http://127.0.0.1:9/api/events/github",
		"[::]:9":         "http://127.0.0.1:9/api/events/github",
	} {
		if got := relayTarget(addr); got != want {
			t.Errorf("relayTarget(%q) = %q, want %q", addr, got, want)
		}
	}
	t.Setenv(eventsRelayEnv, "https://smee.io/from-the-environment")
	cfg, err := parseServeArgs([]string{"--db", filepath.Join(t.TempDir(), "abom.db")}, io.Discard)
	if err != nil || cfg.eventsRelay != "https://smee.io/from-the-environment" {
		t.Errorf("the relay from the environment = %q, %v", cfg.eventsRelay, err)
	}
	cfg, _ = parseServeArgs([]string{"--db", "x.db", "--events-relay", "https://smee.io/named"}, io.Discard)
	if cfg.eventsRelay != "https://smee.io/named" {
		t.Errorf("a named relay = %q, want the flag over the environment", cfg.eventsRelay)
	}
	cfg.webhookSecret = nil
	if err := startRelay(context.Background(), cfg, io.Discard); err == nil {
		t.Error("a relay with no webhook secret to verify its deliveries was started")
	}
}

// installationsOnly is a GitHub that knows one App installed on has-ghas, and nothing about anyone.
func installationsOnly(t *testing.T) (*ghapp.App, func(token string) map[string]string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	carries := map[string]map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
			_, _ = w.Write([]byte(`[{"id":11,"account":{"login":"has-ghas","type":"Organization"},` +
				`"repository_selection":"all","permissions":{"metadata":"read","contents":"read",` +
				`"administration":"read","organization_copilot_requests":"read"}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/11/access_tokens":
			var body struct {
				Permissions map[string]string `json:"permissions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			token := fmt.Sprintf("ghs_%d", len(carries)+1)
			carries[token] = body.Permissions
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, token, time.Now().Add(time.Hour).Format(time.RFC3339))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	app := &ghapp.App{API: srv.URL, HTTP: srv.Client(), Credentials: ghapp.Credentials{AppID: 4242,
		Slug: "abom-has-ghas", Owner: "has-ghas", OwnerKind: "organization", Key: key}}
	return app, func(token string) map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return carries[token]
	}
}

// A production pod has no gh session: with an App, no call it makes reads the person's token, and
// each one carries a token minted for it.
func TestWithAnAppNoCallReadsThePersonsToken(t *testing.T) {
	app, carries := installationsOnly(t)
	creds := newCredentials(app, "")
	creds.operator = func(context.Context) string {
		t.Error("the person's token was read with an App configured")
		return "gho_person"
	}
	ctx := context.Background()

	client, _ := creds.snapshotClient(ctx, app.API, []string{"has-ghas/test-repo-1"})
	if client.Token != "" || len(client.Allowed) != 0 {
		t.Errorf("with an App the snapshot client carries %q and approves %v; want no token and no target",
			client.Token, client.Allowed)
	}

	var o authoringOptions
	creds.copilot(&o)
	if o.mint == nil || o.credential.Kind != toolchain.CredentialInstallation || !strings.Contains(o.credential.Source, "has-ghas") {
		t.Fatalf("authoring with an App: mint=%v credential %+v; want an installation token for has-ghas", o.mint != nil, o.credential)
	}
	token, _, err := o.mint(ctx, false)
	if want := map[string]string{"organization_copilot_requests": "read"}; err != nil || !maps.Equal(carries(token), want) {
		t.Errorf("the Copilot token carries %v, %v; want %v", carries(token), err, want)
	}

	id, err := creds.verify()(ctx)
	if err != nil || id.Login != "abom-has-ghas[bot]" || len(id.Accounts) != 1 || id.Accounts[0].Role != ghauth.RoleInstallation {
		t.Errorf("identity with an App = %+v, %v; want the App's installations", id, err)
	}
	if list, read := creds.picker(); list == nil || read == nil {
		t.Error("with an App the picker still lists through gh")
	}
	if role, err := creds.roleReader()(ctx, "has-ghas", "service", "octocat"); err == nil || role != "" {
		t.Errorf("with an App a person's repository role was read: %q, %v", role, err)
	}
}

// The App asks for no write, so a server reading as one writes nothing: a dependency snapshot and
// default setup are the repository's own, and a write target named beside an App is refused at
// startup rather than carried out with somebody else's token.
func TestWithAnAppTheServerWritesNothing(t *testing.T) {
	app, _ := installationsOnly(t)
	for _, cfg := range []serveConfig{{submitTargets: "has-ghas/test-repo-1"}, {enableTargets: "has-ghas/test-repo-1"},
		{enableAccounts: "has-ghas"}} {
		err := newCredentials(app, "").refuseWrites(cfg)
		if err == nil || !strings.Contains(err.Error(), "App") {
			t.Errorf("write targets %q %q %q beside an App = %v; want the server refused",
				cfg.submitTargets, cfg.enableTargets, cfg.enableAccounts, err)
		}
	}
	if err := newCredentials(app, "").refuseWrites(serveConfig{}); err != nil {
		t.Errorf("an App with no write targets was refused: %v", err)
	}
	if err := newCredentials(nil, "").refuseWrites(serveConfig{submitTargets: "x/y", enableTargets: "x/z"}); err != nil {
		t.Errorf("without an App the operator's write targets were refused: %v", err)
	}
}

// An App owned by a user has no organization to bill, so Copilot is refused with what to name.
func TestAnAppOwnedByAUserNamesNoOrganizationToBill(t *testing.T) {
	app, _ := installationsOnly(t)
	app.Credentials.Owner, app.Credentials.OwnerKind = "data-douser", "user"
	var o authoringOptions
	newCredentials(app, "").copilot(&o)
	if _, _, err := o.mint(context.Background(), false); err == nil || !strings.Contains(err.Error(), "--authoring-org") {
		t.Errorf("minting with no organization to bill = %v; want a refusal naming --authoring-org", err)
	}
	if !strings.Contains(o.credential.Remedy, "--authoring-org") {
		t.Errorf("readiness remedy %q does not say how to name the organization", o.credential.Remedy)
	}
}

// Without an App, the local and development path keeps the operator's token for every call.
func TestWithoutAnAppEveryCallKeepsTheOperatorsToken(t *testing.T) {
	creds := newCredentials(nil, "")
	reads := 0
	creds.operator = func(context.Context) string { reads++; return "gho_person" }
	ctx := context.Background()

	if client, _ := creds.snapshotClient(ctx, ghapp.DefaultAPIURL, []string{"x/y"}); client.Token != "gho_person" || !client.Allowed["x/y"] {
		t.Errorf("the snapshot client carries %q and approves %v; want the operator's token and target", client.Token, client.Allowed)
	}
	if p := creds.patcher(ctx, ghapp.DefaultAPIURL, nil); p.Token != "gho_person" {
		t.Errorf("the default setup writer carries %q; want the operator's token", p.Token)
	}
	var o authoringOptions
	creds.copilot(&o)
	if o.mint != nil || o.credential.Kind != "" {
		t.Errorf("authoring without an App mints (%v) or names %+v; want gh, as before", o.mint != nil, o.credential)
	}
	if list, read := creds.picker(); list != nil || read != nil {
		t.Error("without an App the picker does not list through gh")
	}
	if reads == 0 {
		t.Error("the operator's token was never read on the path that uses it")
	}
}

// A command run in the coordinator's pod names a PostgreSQL database, which has nothing beside it,
// so it reads as the App the pod's environment names, as serve does; never as a gh nobody installed.
func TestACommandInAPodReadsAsTheAppItsEnvironmentNames(t *testing.T) {
	const postgres = "postgres://abom@db.abom:5432/abom?sslmode=verify-full"
	t.Setenv(appDirEnv, "")
	if got := readerAppDir(postgres); got != "" {
		t.Errorf("with no App named, a PostgreSQL database reads as %q; want none", got)
	}
	t.Setenv(appDirEnv, "/run/abom/github-app")
	if got := readerAppDir(postgres); got != "/run/abom/github-app" {
		t.Errorf("readerAppDir = %q; want the directory the environment names", got)
	}
	if got := readerAppDir("/data/abom.db"); got != "/run/abom/github-app" {
		t.Errorf("readerAppDir = %q; the environment names the App over the one beside a database", got)
	}
}
