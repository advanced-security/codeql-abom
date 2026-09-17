package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A dead server must not swallow the answer. Re-running as a fresh process is safe here only
// because the commands routed through the server have no side effects, and it is necessary
// because a rejected command and a crashed JVM are the same event on this protocol.
func TestASmallCommandFallsBackToAFreshProcessWhenTheServerIsGone(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo '{\"id\":\"abom/edges\"}'\n")
	var attempts atomic.Int32

	r := &Runner{CodeQLPath: stub, Server: &CLIServer{
		transport: func([]string) ([]byte, error) {
			attempts.Add(1)
			return nil, ErrServerUnavailable
		},
	}}

	md, err := r.ResolveMetadata(context.Background(), "a.ql")
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if md.ID != "abom/edges" {
		t.Errorf("ID = %q, want the answer the fresh process gave", md.ID)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("the server was tried %d times, want once before falling back", got)
	}
}

// The whole point is that the second call does not pay for a process. A fallback that fired on
// every call would be a slower version of what was there before, and would look identical.
func TestASmallCommandPrefersTheServer(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "echo '{\"id\":\"from-a-process\"}'\n")
	r := &Runner{CodeQLPath: stub, Server: &CLIServer{
		transport: func([]string) ([]byte, error) {
			return []byte(`{"id":"from-the-server"}`), nil
		},
	}}

	md, err := r.ResolveMetadata(context.Background(), "a.ql")
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if md.ID != "from-the-server" {
		t.Errorf("ID = %q, want the server's answer", md.ID)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("a fresh process ran anyway, so nothing was saved")
	}
}

// With no server configured the runner has to behave exactly as it did before, or every caller
// that never opted in would break.
func TestWithoutAServerTheRunnerStillUsesAProcess(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo '{\"id\":\"abom/edges\"}'\n")
	r := &Runner{CodeQLPath: stub}

	md, err := r.ResolveMetadata(context.Background(), "a.ql")
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if md.ID != "abom/edges" {
		t.Errorf("ID = %q, want the process answer", md.ID)
	}
}

// A command the CLI genuinely rejects has to surface as a failure, not as a silent empty answer.
// On this protocol that arrives looking exactly like a crash, so the fresh process is what turns
// it back into a diagnosis.
func TestACommandTheCLIRejectsIsReportedAfterTheFallback(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'no such query' >&2\nexit 2\n")
	r := &Runner{CodeQLPath: stub, Server: &CLIServer{
		transport: func([]string) ([]byte, error) { return nil, ErrServerUnavailable },
	}}

	_, err := r.ResolveMetadata(context.Background(), filepath.Join("nope", "missing.ql"))
	if err == nil {
		t.Fatal("a rejected command reported success")
	}
	if !strings.Contains(err.Error(), "no such query") {
		t.Errorf("error %q does not carry the CLI's diagnostic", err)
	}
}
