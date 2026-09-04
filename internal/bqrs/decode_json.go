package bqrs

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// DecodeToJSON writes the JSON form of a BQRS file to w.
//
// The CLI's stdout is streamed straight to the destination rather than buffered, so decoding a
// result set larger than memory stays possible.
func DecodeToJSON(ctx context.Context, codeqlPath, bqrsPath string, w io.Writer) error {
	if codeqlPath == "" {
		codeqlPath = "codeql"
	}
	cmd := exec.CommandContext(ctx, codeqlPath,
		"bqrs", "decode", "--format=json", "--entities=all", bqrsPath)
	cmd.Stdout = w

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("piping codeql stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting codeql bqrs decode: %w", err)
	}
	// Read stderr before Wait so a large diagnostic cannot deadlock the child on a full pipe.
	diagnostic, _ := io.ReadAll(io.LimitReader(stderr, 4096))

	if err := cmd.Wait(); err != nil {
		if len(diagnostic) > 0 {
			return fmt.Errorf("codeql bqrs decode %s: %w: %s", bqrsPath, err, diagnostic)
		}
		return fmt.Errorf("codeql bqrs decode %s: %w", bqrsPath, err)
	}
	return nil
}
