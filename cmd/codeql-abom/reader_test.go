package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestAReaderDoesNotCompeteForTheCoordinatorOrPrimeItsQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.db")
	s, err := store.OpenBackend(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, held, err := s.AcquireLease("discovery", "the-coordinator", time.Minute); err != nil || !held {
		t.Fatal("coordinator did not acquire its lease", err)
	}
	before, err := s.DatabaseState()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runReader(ctx, serveConfig{dbPath: path, addr: "127.0.0.1:0"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err := s.DatabaseState()
	if err != nil || before != after {
		t.Fatalf("a reader changed persistent state: %v", err)
	}
}

func TestAReaderCannotBeConfiguredToStartDiscovery(t *testing.T) {
	_, err := parseServeArgs([]string{"--db", "local.db", "--reader-only", "--expand"}, io.Discard)
	if err == nil {
		t.Fatal("reader-only accepted expansion")
	}
}

// A reader verifies the declaration with the key directory it is given, as the coordinator does, or
// it shows a decided declaration as unverified and every owned figure as empty.
func TestAReaderVerifiesTheDeclarationWithTheKeyDirectoryItIsGiven(t *testing.T) {
	dir := t.TempDir()
	path, keys := filepath.Join(dir, "abom.db"), filepath.Join(dir, "keys-elsewhere")
	s, err := store.OpenBackend(path, store.Options{AuthzDir: keys})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "acme", Kind: store.AccountOrganization, Enabled: true}},
		store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	srv, closeReader, err := openReader(serveConfig{dbPath: path, authzDir: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader()
	accounts, verdict, err := srv.Store.VerifiedScope()
	if err != nil || verdict.State != store.ScopeSigned || len(accounts) != 1 {
		t.Errorf("the reader judged the declaration %+v with %d accounts, %v; want it signed", verdict, len(accounts), err)
	}
}
