package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

func TestRemoteContentCannotBypassPrivateSourceOwnership(t *testing.T) {
	s := gateStore(t)
	var downloads int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/elsewhere/private" {
			io.WriteString(w, `{"full_name":"elsewhere/private","visibility":"private"}`)
			return
		}
		downloads++
		io.WriteString(w, `{"type":"file","encoding":"base64","content":"YQ=="}`)
	}))
	defer api.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	c := githubClient(context.Background(), api.URL, s, nil)
	for name, read := range map[string]func() error{
		"database": func() error {
			return c.DownloadDatabase(context.Background(), "elsewhere/private", "actions", filepath.Join(t.TempDir(), "db.zip"))
		},
		"analysis": func() error {
			return c.DownloadAnalysis(context.Background(), "elsewhere/private", 1, filepath.Join(t.TempDir(), "findings.sarif"))
		},
		"configuration": func() error {
			_, err := c.FileContent(context.Background(), "elsewhere/private", "config.yml", "main")
			return err
		},
		"example": func() error {
			_, err := c.CommitFiles(context.Background(), "elsewhere/private", "sha", []string{"ci.yml"})
			return err
		},
		"alerts": func() error {
			_, err := c.CodeScanningAlerts(context.Background(), "elsewhere/private")
			return err
		},
		"comparison": func() error {
			_, err := c.CompareCommits(context.Background(), "elsewhere/private", "base", "head")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(); err == nil || !strings.Contains(err.Error(), "source policy") {
				t.Errorf("remote read = %v, want private-source policy refusal", err)
			}
		})
	}
	if downloads != 0 {
		t.Fatalf("%d remote content requests escaped the ownership check", downloads)
	}
}

func TestExpansionRefusesDisabledPrivateSourcesBeforeInvokingGit(t *testing.T) {
	s := gateStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"full_name":"acme/private","visibility":"private",
			"security_and_analysis":{"code_security":{"status":"disabled"}}}`)
	}))
	defer api.Close()
	t.Setenv("GITHUB_API_URL", api.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	marker := filepath.Join(dir, "git-called")
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	e, cache, closeRunner, err := newExpander(s, nil, expandAllConfig{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRunner()
	defer cache.Release()
	_, err = e.Fetcher.Fetch(context.Background(), filepath.Join(dir, "src"), "acme", "private", "main")
	if err == nil || !strings.Contains(err.Error(), "source policy") {
		t.Errorf("fetch = %v, want source policy refusal", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("git was invoked before licensing was checked: %v", err)
	}
}

func TestSecurityEventsRefreshSourcePolicyEvenWithDatabaseReuseDisabled(t *testing.T) {
	s := gateStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertOwnedRepository("acme/repo"); err != nil {
		t.Fatal(err)
	}
	q := &frontier.Queue{Store: s}
	key := frontier.Key{NodeKey: "acme/repo"}
	if _, err := q.Enqueue([]frontier.Item{{NodeKey: key.NodeKey, Owner: "acme", Repo: "repo"}}); err != nil {
		t.Fatal(err)
	}
	enabled := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/repo":
			status := "disabled"
			if enabled {
				status = "enabled"
			}
			io.WriteString(w, `{"full_name":"acme/repo","visibility":"private","security_and_analysis":{"code_security":{"status":"`+status+`"}}}`)
		case "/repos/acme/repo/code-scanning/default-setup":
			io.WriteString(w, `{"state":"configured","languages":["go"]}`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer api.Close()
	reader := catalog.New(api.URL, "", nil)
	policy := protectSources(s, reader, io.Discard)
	err := policy.Check(context.Background(), "acme/repo")
	if !sourcepolicy.IsDenied(err) {
		t.Fatalf("initial decision = %v", err)
	}
	if err := q.Unavailable(key, err.Error()); err != nil {
		t.Fatal(err)
	}
	loop := &supervisor.Supervisor{Store: s, Queue: q, Expander: &expand.Expander{SourcePolicy: policy}}
	processor := newProcessor(s, loop, reader, nil, 0, io.Discard)
	enabled = true
	if err := processor.Setup(context.Background(), "acme/repo"); err != nil {
		t.Fatal(err)
	}
	stats, err := q.Stats()
	if err != nil || stats[frontier.StatusPending] != 1 {
		t.Fatalf("queue after configuration changed: %v, %v", stats, err)
	}
}

func TestThePolicyAndGitFetchCannotReferToDifferentGitHubHosts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_API_URL", "https://github.example/api/v3")
	e, cache, closeRunner, err := newExpander(gateStore(t), nil, expandAllConfig{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Release()
	defer closeRunner()
	checkout, ok := e.Fetcher.(*expand.Checkout)
	if !ok || checkout.BaseURL != "https://github.example" {
		t.Fatalf("checkout=%+v, want the host whose policy was checked", checkout)
	}
}
