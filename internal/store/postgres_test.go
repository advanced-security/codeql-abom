package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresURLsAreNotTreatedAsSQLitePaths(t *testing.T) {
	for _, scheme := range []string{"postgres", "postgresql"} {
		t.Run(scheme, func(t *testing.T) {
			s, err := OpenBackend(scheme+"://invalid:invalid@127.0.0.1:1/abom?connect_timeout=1&sslmode=disable", Options{})
			if s != nil {
				_ = s.Close()
				t.Fatal("unreachable PostgreSQL server returned a backend")
			}
			if err == nil || strings.Contains(err.Error(), "unable to open database file") {
				t.Fatalf("PostgreSQL URL reached SQLite: %v", err)
			}
		})
	}
}

func TestSQLitePlainBackendPathsRetainPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := OpenBackend(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(b), "SQLite format 3") {
		t.Fatalf("plain path did not create SQLite: %v", err)
	}
}
