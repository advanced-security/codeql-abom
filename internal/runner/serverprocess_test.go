package runner

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestBothServersRestartAndKeepTheFirstDiagnosticLine(t *testing.T) {
	for _, evaluator := range []bool{false, true} {
		t.Run(map[bool]string{false: "CLI", true: "evaluator"}[evaluator], func(t *testing.T) {
			path, argsFile := stubCodeQL(t, "cat >/dev/null\nprintf 'problem\\nstack trace\\n' >&2\n")
			c := &CLIServer{CodeQLPath: path}
			q := &QueryServer{CodeQLPath: path, Threads: 2, RAMMB: 512}
			start, stop := c.start, c.stop
			want := "execute cli-server"
			if evaluator {
				start, stop = q.start, q.stop
				want = "execute query-server2 --threads=2 --ram=512"
			}
			defer stop()
			if got := stop(); got != "" {
				t.Fatalf("stopping an idle server: %q", got)
			}
			for range 2 {
				if err := start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if got := stop(); got != ": problem" {
					t.Fatalf("stop = %q, want the first diagnostic line", got)
				}
				args, err := os.ReadFile(argsFile)
				if err != nil || strings.TrimSpace(string(args)) != want {
					t.Fatalf("argv = %q, %v; want %q", args, err, want)
				}
				if got := stop(); got != "" {
					t.Fatalf("stopping again reused a diagnostic: %q", got)
				}
			}
		})
	}
}
