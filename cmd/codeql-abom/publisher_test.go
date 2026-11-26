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

func savedPublisher(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c := ghapp.Conversion{ID: 77, Slug: "abom-publisher-acme", ClientID: "Iv23liPUB",
		PEM:     string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		HTMLURL: "https://github.com/apps/abom-publisher-acme"}
	c.Owner.Login, c.Owner.Type = "acme", "Organization"
	dir := filepath.Join(t.TempDir(), "github-app-publisher")
	if _, err := ghapp.Save(dir, c, "", "https://api.github.com"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A publisher installed more broadly than one repository stops serve, because it would hold a
// write ABOM must not; one whose installations could not be read is kept and asked again at
// each export, because that says nothing about how it is installed.
func TestServeRefusesAMisinstalledPublisherAndKeepsAnUnreadableOne(t *testing.T) {
	if p, err := publisherFor(context.Background(), filepath.Join(t.TempDir(), "none"), "", &strings.Builder{}); p != nil || err != nil {
		t.Errorf("no publisher configured = %v, %v; want none and no error", p, err)
	}

	dir := savedPublisher(t)
	everywhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "account": map[string]any{"login": "acme"},
			"repository_selection": "all", "permissions": map[string]string{
				"contents": "write", "pull_requests": "write", "metadata": "read"}}})
	}))
	defer everywhere.Close()
	if p, err := publisherFor(context.Background(), dir, everywhere.URL, &strings.Builder{}); p != nil ||
		err == nil || !strings.Contains(err.Error(), "every repository") {
		t.Errorf("a publisher on every repository = %v, %v; want serve refused naming why", p, err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer down.Close()
	var log strings.Builder
	if p, err := publisherFor(context.Background(), dir, down.URL, &log); p == nil || err != nil ||
		!strings.Contains(log.String(), "each export") {
		t.Errorf("an unreadable listing = %v, %v, log %q; want the publisher kept with a warning", p, err, log.String())
	}
}
