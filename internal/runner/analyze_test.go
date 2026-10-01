package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeDatabaseInvokesCodeQL(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	out := filepath.Join(t.TempDir(), "results.sarif")
	err := r.AnalyzeDatabase(context.Background(), "/db", "codeql/actions-queries:codeql-suites/actions-all.qls", out)
	if err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	args := string(recorded)
	for _, want := range []string{
		"database", "analyze", "/db",
		"actions-all.qls",
		"--format=sarif-latest",
		"--sarif-include-query-help=always",
		"--sarif-add-snippets",
		"--output=" + out,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// The suite emits many alerts per database; without this the run aborts on the first one.
func TestAnalyzeDatabaseToleratesFindings(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	if err := r.AnalyzeDatabase(context.Background(), "/db", "suite.qls",
		filepath.Join(t.TempDir(), "o.sarif")); err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	recorded, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(recorded), "--no-sarif-add-file-contents") {
		t.Error("expected file contents to be omitted from SARIF")
	}
}

// A published suite is named, never vendored. Without this the CLI reports the pack as
// misspelled rather than as absent, on any machine that has not analyzed Actions before.
func TestAnalyzeDatabaseFetchesMissingQueryPacks(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	if err := r.AnalyzeDatabase(context.Background(), "/db", "codeql/actions-queries:codeql-suites/actions-all.qls",
		filepath.Join(t.TempDir(), "o.sarif")); err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	if !strings.Contains(string(mustRead(t, argsFile)), "--download") {
		t.Error("expected the suite's query pack to be downloaded when absent")
	}
}

func TestAnalyzeDatabasePassesResourceLimits(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, Threads: 4, RAMMB: 2048}

	if err := r.AnalyzeDatabase(context.Background(), "/db", "suite.qls",
		filepath.Join(t.TempDir(), "o.sarif")); err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	args := string(mustRead(t, argsFile))
	for _, want := range []string{"--threads=4", "--ram=2048"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// Code Scanning analyzes this repository with local sources included, so selfcheck has to ask
// for the same threat model or the two disagree about what a finding is and the gate passes on a
// narrower question than the one being answered upstream.
func TestAnalyzeDatabasePassesTheThreatModel(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, ThreatModel: "local"}

	if err := r.AnalyzeDatabase(context.Background(), "/db", "suite.qls",
		filepath.Join(t.TempDir(), "o.sarif")); err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	if args := string(mustRead(t, argsFile)); !strings.Contains(args, "--threat-model=local") {
		t.Errorf("args %q missing --threat-model=local", args)
	}
}

// Unset means "whatever the CLI defaults to". Passing an empty value instead would name a threat
// model of "", which the CLI rejects, so every caller that never set one would stop working.
func TestAnUnsetThreatModelIsNotPassedAtAll(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	if err := r.AnalyzeDatabase(context.Background(), "/db", "suite.qls",
		filepath.Join(t.TempDir(), "o.sarif")); err != nil {
		t.Fatalf("AnalyzeDatabase: %v", err)
	}

	if args := string(mustRead(t, argsFile)); strings.Contains(args, "--threat-model") {
		t.Errorf("args %q names a threat model that was never chosen", args)
	}
}

func TestAnalyzeDatabasePropagatesFailure(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'suite not found' >&2\nexit 3\n")
	r := &Runner{CodeQLPath: stub}

	err := r.AnalyzeDatabase(context.Background(), "/db", "nope.qls",
		filepath.Join(t.TempDir(), "o.sarif"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "suite not found") {
		t.Errorf("error did not include stderr: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}
