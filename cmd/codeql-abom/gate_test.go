package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Discovery crawls whatever it is pointed at, so pointing it at nothing in particular is how a
// corpus of 204 repositories nobody owns gets built. The scope has to be stated first.
func TestDiscoveryWillNotStartWithNothingDeclared(t *testing.T) {
	s := gateStore(t)

	problem := preflight(s, "", "", t.TempDir())()

	if problem == nil {
		t.Fatal("nothing is declared, so there is no scope to crawl")
	}
	if !strings.Contains(problem.Reason, "owned") {
		t.Errorf("Reason = %q, want it to name what is missing", problem.Reason)
	}
	if problem.Remedy == "" {
		t.Error("a refusal with no remedy is a control that says no and not how")
	}
}

// Declared-but-disabled is the same answer to the only question the gate asks. The operator's own
// account starts this way, so this is the state a first run is actually in.
func TestDiscoveryWillNotStartWhenEveryDeclarationIsDisabled(t *testing.T) {
	s := gateStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "data-douser", Kind: store.AccountUser, Enabled: false},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	if preflight(s, "", "", t.TempDir())() == nil {
		t.Error("every declaration is disabled, so nothing is in scope")
	}
}

func TestDiscoveryStartsOnceSomethingIsDeclared(t *testing.T) {
	s := gateStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	stubToolchain(t)

	if problem := preflight(s, "", "", t.TempDir())(); problem != nil {
		t.Errorf("a declared scope should start: %s (%s)", problem.Reason, problem.Remedy)
	}
}

// stubToolchain puts a `codeql` and a `git` on a PATH holding nothing else.
//
// The gate checks the toolchain as well as the scope, so a test that a declared scope starts has
// to say which tools it assumes. Reading them off the machine made this assert "a dev workstation
// has enough installed", which is true there and false on a runner. preflight only looks the two
// up, so neither stub has to do anything.
func stubToolchain(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is posix-only")
	}

	dir := t.TempDir()
	for _, tool := range []string{"codeql", "git"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("writing the %s stub: %v", tool, err)
		}
	}
	t.Setenv("PATH", dir)
}

// The scope is checked before the tools, because "declare what you own" is something the operator
// can act on immediately and a missing CodeQL CLI on a machine that has one is a confusing answer
// to a question about configuration.
func TestTheScopeIsRefusedBeforeTheToolchain(t *testing.T) {
	s := gateStore(t)

	problem := preflight(s, "/nonexistent/codeql", "", t.TempDir())()

	if problem == nil {
		t.Fatal("neither the scope nor the CLI is available")
	}
	if strings.Contains(problem.Reason, "CodeQL") {
		t.Errorf("Reason = %q, want the scope reported first", problem.Reason)
	}
}

func gateStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}
