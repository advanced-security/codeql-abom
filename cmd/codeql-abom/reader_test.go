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
