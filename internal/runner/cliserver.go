package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ErrServerUnavailable marks a failure of the server rather than a considered answer from it.
//
// Every failure is one of these, because a command the CLI rejects takes the whole server down
// with it: the answer comes back empty and the process exits 2. There is therefore no way to
// tell "this query is bad" from "the JVM died" at this layer, which is why the caller re-runs as
// a fresh process to find out. That is only safe because the commands routed here have no side
// effects.
var ErrServerUnavailable = errors.New("the CodeQL server is unavailable")

// CLIServer runs CodeQL's small commands through one long-lived JVM.
//
// A fresh `codeql` process spends about 0.7s starting the JVM before it does any work, which is
// most of what a `resolve metadata` or a `bqrs decode` costs. Through the server the same calls
// answer in about 0.004s. A crawl pays that per query per round and per decode per repository, so
// it is the difference between minutes and seconds across a round.
//
// Not everything can come here. Commands that spawn extractors, stream multi-event output, or are
// compound orchestration have to be their own process, and the server's working directory and
// environment are fixed when it starts.
type CLIServer struct {
	// CodeQLPath is the CLI to invoke. Empty means "codeql" from PATH.
	CodeQLPath string

	// One command at a time: the protocol is a single stream of NUL-delimited answers, so a
	// second command in flight would read the first one's response.
	mu sync.Mutex
	serverProcess

	// transport replaces the process for tests.
	transport func(args []string) ([]byte, error)
}

// Run executes one CLI command and returns its stdout.
func (c *CLIServer) Run(ctx context.Context, args ...string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.transport != nil {
		return c.transport(args)
	}
	if err := c.start(ctx); err != nil {
		return nil, err
	}
	if err := writeCommand(c.stdin, args); err != nil {
		diagnostic := c.stop()
		return nil, fmt.Errorf("%w: sending %q%s: %w",
			ErrServerUnavailable, strings.Join(args, " "), diagnostic, err)
	}
	out, err := readResponse(c.stdout)
	if err != nil {
		// A desynchronized or dead stream cannot be recovered by reading further, so the process
		// goes and the next call starts a fresh one.
		diagnostic := c.stop()
		return nil, fmt.Errorf("%w: running %q%s: %w",
			ErrServerUnavailable, strings.Join(args, " "), diagnostic, err)
	}
	return out, nil
}

// Close shuts the server down. Closing stdin is what ends it; the CLI exits when its input does.
func (c *CLIServer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stop()
	return nil
}

func (c *CLIServer) start(ctx context.Context) error {
	return c.startProcess(ctx, c.CodeQLPath, "execute", "cli-server")
}

// serverProcess owns the common lifecycle; each protocol holds its lock across a whole exchange.
type serverProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *bytes.Buffer
	started bool
}

func (p *serverProcess) startProcess(ctx context.Context, path string, args ...string) error {
	if p.started {
		return nil
	}
	if path == "" {
		path = "codeql"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%w: piping to it: %w", ErrServerUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: piping from it: %w", ErrServerUnavailable, err)
	}
	// The diagnostic for a rejected command goes to stderr and the answer comes back empty, so
	// without this a failure would be reported as "the server stopped" and nothing more.
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: starting it: %w", ErrServerUnavailable, err)
	}

	p.cmd, p.stdin, p.stdout, p.stderr, p.started = cmd, stdin, bufio.NewReader(stdout), &diagnostic, true
	return nil
}

// stop ends the server and returns whatever it said on the way out.
func (p *serverProcess) stop() string {
	if !p.started {
		return ""
	}
	p.stdin.Close()
	_ = p.cmd.Wait()

	diagnostic := strings.TrimSpace(p.stderr.String())
	if diagnostic != "" {
		diagnostic = ": " + lastLine(diagnostic)
	}
	*p = serverProcess{}
	return diagnostic
}

// lastLine keeps the line that names the problem. CodeQL prefixes a stack trace with it and the
// rest is noise in a crawl log.
func lastLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// writeCommand frames one command: the argv as a JSON array, then a NUL.
func writeCommand(w io.Writer, args []string) error {
	encoded, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	_, err = w.Write([]byte{0})
	return err
}

// readResponse reads one NUL-terminated answer.
//
// A stream that ends without the terminator is an error rather than a short answer: the CLI died
// mid-write, and returning what arrived would look like a successful empty result.
func readResponse(r *bufio.Reader) ([]byte, error) {
	out, err := r.ReadBytes(0)
	if err != nil {
		if len(out) > 0 {
			return nil, fmt.Errorf("the CodeQL server stopped mid-answer: %w", err)
		}
		return nil, err
	}
	return out[:len(out)-1], nil
}
