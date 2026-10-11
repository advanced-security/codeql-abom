package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteRelativeReadOnlyPathsKeepTheirReaderBehavior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader fixture #1.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRepository("reader/source", ""); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenBackend(relative, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got, err := reader.Summary(); err != nil || got.SeedRepos != 1 {
		t.Fatalf("relative reader summary=%+v error=%v", got, err)
	}
	if _, err := reader.UpsertRepository("reader/forbidden", ""); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("relative reader admitted a write: %v", err)
	}
}
