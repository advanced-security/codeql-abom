package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateDatabaseInvokesCodeQL(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	for _, want := range []string{
		"database", "create", "--language=actions", "--source-root=/src", db,
	} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// A stale directory would make codeql refuse to write, so re-analyzing a drifted dependency has
// to start from a clean location.
func TestCreateDatabaseReplacesExistingDirectory(t *testing.T) {
	stub, _ := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatalf("seeding directory: %v", err)
	}
	stale := filepath.Join(db, "stale.txt")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale database contents survived")
	}
}

func TestCreateDatabasePassesResourceLimits(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, Threads: 3, RAMMB: 2048}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--threads=3", "--ram=2048"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// A repository with no workflows and no action manifest yields no database, and that is a
// permanent answer rather than a failure to retry.
func TestCreateDatabaseReportsNoSourceCode(t *testing.T) {
	stub, _ := stubCodeQL(t,
		"echo 'No source code was seen during the build' >&2\nexit 32\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "actions")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsNoSourceCode(err) {
		t.Errorf("IsNoSourceCode(%v) = false, want true", err)
	}
}

func TestCreateDatabasePropagatesFailure(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'disk full' >&2\nexit 1\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "actions")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsNoSourceCode(err) {
		t.Error("a generic failure must not be reported as empty source")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error %v does not carry the CLI message", err)
	}
}
