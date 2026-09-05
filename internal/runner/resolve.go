package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Metadata is what a query declares about itself.
//
// How results must be read is a property of the query, not of ABOM's opinion about its filename.
// Reading it from the CLI is what lets a query be added to a suite and ingested correctly with
// no change to the ingester — the point of an inspection engine that is meant to be extended.
type Metadata struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is "problem", "path-problem", "graph", or empty. Empty means a table query, which
	// yields BQRS and no SARIF, and is how the dependency extraction queries are told apart
	// from the security ones.
	Kind string `json:"kind"`
	// Tags arrive space-separated, as the CLI prints them.
	Tags string `json:"tags"`
}

// HasTag reports whether the query declares a tag, matching whole tags only.
func (m Metadata) HasTag(tag string) bool {
	for _, t := range strings.Fields(m.Tags) {
		if t == tag {
			return true
		}
	}
	return false
}

// ResolveQueries expands a suite into the queries it selects.
//
// The suite is the unit that grows: new queries are added by editing a `.qls`, so the query set
// has to be discovered at run time. Listing it in Go would mean a release for every query added.
func (r *Runner) ResolveQueries(ctx context.Context, suite string) ([]string, error) {
	out, err := r.output(ctx, "resolve", "queries", suite)
	if err != nil {
		return nil, fmt.Errorf("codeql resolve queries %s: %w", suite, err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// ResolveMetadata reads what a query declares about itself.
func (r *Runner) ResolveMetadata(ctx context.Context, queryPath string) (Metadata, error) {
	out, err := r.output(ctx, "resolve", "metadata", "--format=json", queryPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("codeql resolve metadata %s: %w", queryPath, err)
	}
	var md Metadata
	if err := json.Unmarshal(bytes.TrimSpace(out), &md); err != nil {
		return Metadata{}, fmt.Errorf("reading metadata for %s: %w", queryPath, err)
	}
	return md, nil
}

// output runs the CLI and returns stdout, folding stderr into the error so a failure explains
// itself rather than surfacing a bare exit status.
func (r *Runner) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}
