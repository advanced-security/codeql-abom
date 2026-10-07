package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/store"
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
