package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Narrowing what is owned stops the crawl spending on what left: the queue the server runs claims
// only inside the owned closure, and the rows outside stay on record.
func TestTheServedCrawlClaimsOnlyInsideTheOwnedClosure(t *testing.T) {
	s := servedStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertOwnedRepository("octo/mine"); err != nil {
		t.Fatal(err)
	}
	loop, err := newSupervisor(s, discoveryOptions{workDir: t.TempDir()}, nil, io.Discard)
	if err != nil {
		t.Fatalf("newSupervisor: %v", err)
	}
	if _, err := loop.Queue.Enqueue([]frontier.Item{
		{NodeKey: "octo/mine", Owner: "octo", Repo: "mine", Depth: 0},
		{NodeKey: "left/behind", Owner: "left", Repo: "behind", Depth: 1},
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := loop.Queue.Claim(10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].NodeKey != "octo/mine" {
		t.Errorf("claimed %+v, want only the owned repository", claimed)
	}
}

// The objective and the pool estimates describe the crawl, so the server measures inside the same
// closure the crawl claims in.
func TestTheServerMeasuresInsideTheClosureTheCrawlClaimsIn(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	s := servedStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertOwnedRepository("octo/mine"); err != nil {
		t.Fatal(err)
	}
	loop, err := newSupervisor(s, discoveryOptions{workDir: t.TempDir()}, nil, io.Discard)
	if err != nil {
		t.Fatalf("newSupervisor: %v", err)
	}
	srv, err := newConfiguredServer(context.Background(), serveConfig{}, s, loop, nil, io.Discard)
	if err != nil {
		t.Fatalf("newConfiguredServer: %v", err)
	}
	if srv.QueueClosure == nil {
		t.Fatal("the server measures the whole queue while the crawl claims inside the closure")
	}
	names, err := srv.QueueClosure()
	if err != nil || len(names) != 1 || names[0] != "octo/mine" {
		t.Errorf("the server's closure = %v, %v; want the owned repository", names, err)
	}
}

// A repository read in full that declares nothing was not a failure: the log says what was read.
// A real failure still says FAILED.
func TestTheDiscoveryLogDoesNotCallNothingToAnalyzeAFailure(t *testing.T) {
	var log bytes.Buffer
	logged := logDiscoveryFailures(&log)

	logged(work.Event{Kind: work.EventFailed, Job: work.Job{Repository: "octo/docs"},
		Err: &expand.NothingToAnalyze{Why: "docs has no workflows or action manifest"}})
	logged(work.Event{Kind: work.EventFailed, Job: work.Job{Repository: "octo/app"},
		Err: errors.New("codeql database create: exit status 32")})

	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "FAILED") || strings.Contains(lines[0], "unavailable") ||
		!strings.Contains(lines[0], "read octo/docs: docs has no workflows or action manifest") {
		t.Errorf("log = %q, want the read stated without calling it a failure", log.String())
	}
	if !strings.Contains(lines[len(lines)-1], "FAILED octo/app") {
		t.Errorf("log = %q, want the real failure still called one", log.String())
	}
}
