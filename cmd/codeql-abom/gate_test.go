package main

import (
	"path/filepath"
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
	}); err != nil {
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
	}); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	if problem := preflight(s, "", "", t.TempDir())(); problem != nil {
		t.Errorf("a declared scope should start: %s (%s)", problem.Reason, problem.Remedy)
	}
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
