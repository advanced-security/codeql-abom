package runner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("adding %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return path
}

// A CodeQL database archive arrives from a remote API, so a crafted entry name must never be
// able to write outside the extraction directory.
func TestExtractRejectsPathTraversal(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"../../escaped.txt": "owned",
	})
	dest := t.TempDir()

	if _, err := Extract(zipPath, dest); err == nil {
		t.Fatal("expected an error for a traversing entry")
	}

	outside := filepath.Join(filepath.Dir(dest), "escaped.txt")
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("entry escaped to %s", outside)
	}
}

func TestExtractRejectsAbsolutePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix absolute paths")
	}
	zipPath := writeZip(t, map[string]string{"/tmp/abom-absolute.txt": "owned"})

	if _, err := Extract(zipPath, t.TempDir()); err == nil {
		t.Fatal("expected an error for an absolute entry")
	}
}

// An entry with no name resolves to the destination directory itself, and extracting it means
// opening a directory for writing. That surfaces as "is a directory" from the copy, which reads
// as a broken extractor rather than as the malformed archive it is.
func TestSafeJoinRejectsAnEntryThatNamesNoFile(t *testing.T) {
	root := t.TempDir()

	if target, err := safeJoin(root, ""); err == nil {
		t.Errorf("safeJoin(root, %q) = %q, want an error", "", target)
	}
}

func TestExtractFindsDatabaseRoot(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"actions-database/codeql-database.yml":  "sourceLocationPrefix: /src",
		"actions-database/src.zip":              "source",
		"actions-database/db-actions/data.trap": "trap",
	})
	dest := t.TempDir()

	dbDir, err := Extract(zipPath, dest)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dbDir, "codeql-database.yml")); err != nil {
		t.Errorf("database root %q has no codeql-database.yml: %v", dbDir, err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "db-actions", "data.trap")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
}

// Some archives place the marker at the top level rather than inside a wrapper directory.
func TestExtractHandlesFlatArchive(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"codeql-database.yml": "sourceLocationPrefix: /src",
	})
	dest := t.TempDir()

	dbDir, err := Extract(zipPath, dest)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if dbDir != dest {
		t.Errorf("dbDir = %q, want %q", dbDir, dest)
	}
}

func TestExtractWithoutDatabaseMarkerFails(t *testing.T) {
	zipPath := writeZip(t, map[string]string{"readme.txt": "not a database"})

	if _, err := Extract(zipPath, t.TempDir()); err == nil {
		t.Fatal("expected an error when no codeql-database.yml is present")
	}
}

// stubCodeQL writes a fake CLI that records its arguments, so query invocation can be verified
// without depending on a real database.
func stubCodeQL(t *testing.T, script string) (path, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is posix-only")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	path = filepath.Join(dir, "codeql")

	body := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n" + script
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	return path, argsFile
}

func TestRunQueryInvokesCodeQL(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	out := filepath.Join(t.TempDir(), "results.bqrs")
	if err := r.RunQuery(context.Background(), "/db", "/queries/AbomEdges.ql", out); err != nil {
		t.Fatalf("RunQuery: %v", err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	args := string(recorded)
	for _, want := range []string{"query", "run", "--database=/db", "--output=" + out, "AbomEdges.ql"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

func TestRunQueryPropagatesFailure(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'compilation failed' >&2\nexit 2\n")
	r := &Runner{CodeQLPath: stub}

	err := r.RunQuery(context.Background(), "/db", "/q.ql", filepath.Join(t.TempDir(), "o.bqrs"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "compilation failed") {
		t.Errorf("error did not include stderr: %v", err)
	}
}

func TestRunQueryRespectsContextCancellation(t *testing.T) {
	stub, _ := stubCodeQL(t, "sleep 30\n")
	r := &Runner{CodeQLPath: stub}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.RunQuery(ctx, "/db", "/q.ql", filepath.Join(t.TempDir(), "o.bqrs")); err == nil {
		t.Fatal("expected an error for a canceled context")
	}
}

func TestRunnerDefaultsToCodeQLOnPath(t *testing.T) {
	if got := (&Runner{}).codeql(); got != "codeql" {
		t.Errorf("codeql() = %q, want %q", got, "codeql")
	}
}
