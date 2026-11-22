package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/advanced-security/codeql-abom/internal/harness"
)

// stdin is what serialize-mcp relays to the server, a variable so a test can feed it.
var stdin io.Reader = os.Stdin

// runSerializeMCP starts the MCP server named after -- and relays its stdio, handing it one tool
// call at a time. Authoring sessions start ql-mcp this way.
func runSerializeMCP(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("serialize-mcp: name the server to start after --")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stderr = stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("serialize-mcp: starting %s: %w", args[0], err)
	}
	relayed := harness.Serialize(ctx, stdin, stdout, in, out)
	if errors.Is(relayed, context.Canceled) {
		relayed = nil
	}
	exited := cmd.Wait()
	if ctx.Err() != nil {
		exited = nil
	}
	return errors.Join(relayed, exited)
}

// relay is the command line that starts an MCP server behind this binary's serialize-mcp; empty
// when this binary cannot be located, which starts the server directly.
func relay() []string {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	return []string{self, "serialize-mcp", "--"}
}
