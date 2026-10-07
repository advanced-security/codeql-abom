package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// An App created with nowhere to deliver would receive nothing, which is the reason to create it.
func TestAppCreateRefusesWithoutADeliveryAddress(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"app", "create", "--owner", "has-ghas",
		"--dir", filepath.Join(t.TempDir(), "github-app")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--events-relay") {
		t.Errorf("create without a delivery address = %v, want a refusal naming --events-relay", err)
	}
	err = run(context.Background(), []string{"app", "create", "--events-relay", "https://smee.io/abc",
		"--dir", filepath.Join(t.TempDir(), "github-app")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--owner") {
		t.Errorf("create without an owner = %v, want a refusal naming --owner", err)
	}
}

// Letting other accounts install the App is a decision made in GitHub's settings for it, never a flag here.
func TestAppCreateOffersNoWayToLetOtherAccountsInstall(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"app", "create", "--public", "--events-relay", "https://smee.io/abc",
		"--dir", filepath.Join(t.TempDir(), "github-app")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not defined: -public") {
		t.Errorf("create --public = %v, want it refused as an option that does not exist", err)
	}
}

// Status names the App, the key's fingerprint and its files, and none of the secrets in them.
func TestAppStatusNamesTheKeyByFingerprintAndPrintsNoSecret(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242})
	}))
	t.Cleanup(github.Close)
	secret := "whsec-status-0123456789"
	c := ghapp.Conversion{ID: 4242, Slug: "abom-has-ghas", ClientID: "Iv23liSTATUS", PEM: pemText,
		WebhookSecret: &secret, HTMLURL: "https://github.com/apps/abom-has-ghas"}
	c.Owner.Login, c.Owner.Type = "has-ghas", "Organization"
	dir := filepath.Join(t.TempDir(), "github-app")
	if _, err := ghapp.Save(dir, c, "https://smee.io/abc", github.URL); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"app", "status", "--dir", dir, "--api-url", github.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("app status: %v\n%s", err, stderr.String())
	}
	out := stdout.String() + stderr.String()
	for _, want := range []string{"abom-has-ghas", "4242", ghapp.Fingerprint(&key.PublicKey), ghapp.KeyFile(dir)} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not name %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(pemText, "\n") {
		if len(line) >= 40 && strings.Contains(out, line) {
			t.Fatalf("status prints the private key:\n%s", out)
		}
	}
	if strings.Contains(out, secret) {
		t.Errorf("status prints the webhook secret:\n%s", out)
	}
}
