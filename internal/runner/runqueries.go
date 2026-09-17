package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Evaluator evaluates a batch of queries against a database without starting a process for it.
type Evaluator interface {
	Evaluate(ctx context.Context, dbPath string, queryPaths []string, outDir string) (map[string]string, error)
}

// RunQueries evaluates every query against one database in a single CodeQL invocation, returning
// the BQRS each wrote keyed by the query path asked for.
//
// One invocation rather than one `query run` per query, because the cost is per *process*: on one
// repository's three graph queries, 11.76 s against 4.34 s, of which 1.47 s is evaluation. A JVM
// start is only 0.72 s of the ~3.4 s each process wastes; the rest is library-path resolution,
// pack resolution, opening the dataset and tearing the evaluator down. It also takes the CodeQL
// lock once instead of three times, and that lock is machine-wide.
func (r *Runner) RunQueries(
	ctx context.Context, dbPath string, queryPaths []string,
) (map[string]string, error) {
	if len(queryPaths) == 0 {
		return map[string]string{}, nil
	}

	// CodeQL names its output after the query file, so two queries sharing a file name are
	// indistinguishable in the results tree. Refused before evaluating rather than after, since
	// the answer cannot be salvaged either way.
	byName := make(map[string]string, len(queryPaths))
	for _, q := range queryPaths {
		name := resultName(q)
		if first, clash := byName[name]; clash {
			return nil, fmt.Errorf(
				"runner: %s and %s share a file name, so their results cannot be told apart",
				first, q)
		}
		byName[name] = q
	}

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	// The results directory belongs to the database rather than to a run, and CodeQL leaves
	// earlier output in place. Cleared once for both paths: without it, a query that reported
	// success and wrote nothing would be answered with whatever a previous invocation left.
	results := filepath.Join(dbPath, "results")
	if err := os.RemoveAll(results); err != nil {
		return nil, fmt.Errorf("clearing previous results: %w", err)
	}

	if r.Evaluator != nil {
		out, err := r.Evaluator.Evaluate(ctx, dbPath, queryPaths, results)
		if err == nil {
			return out, nil
		}
		// Only transport failures fall back. A query the evaluator rejected would be rejected
		// again by a fresh process, at the cost of a JVM start to learn nothing.
		if !errors.Is(err, ErrServerUnavailable) {
			return nil, err
		}
		// A half-finished session may have written some of them, and the fallback has to be able
		// to tell its own output from what it inherited.
		if err := os.RemoveAll(results); err != nil {
			return nil, fmt.Errorf("clearing partial results: %w", err)
		}
	}
	return r.runQueriesInOwnProcess(ctx, dbPath, byName, queryPaths, results)
}

func (r *Runner) runQueriesInOwnProcess(
	ctx context.Context, dbPath string, byName map[string]string, queryPaths []string,
	results string,
) (map[string]string, error) {
	args := []string{"database", "run-queries"}
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}
	// Everything after the separator is data, so a query path is never read as a flag.
	args = append(args, "--", dbPath)
	args = append(args, queryPaths...)

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("codeql database run-queries: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("codeql database run-queries: %w", err)
	}

	written, err := collectBQRS(results)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(byName))
	for name, query := range byName {
		path, ok := written[name]
		if !ok {
			// An empty result set is still a file, so nothing here means the query did not run.
			return nil, fmt.Errorf("codeql database run-queries: %s produced no results", query)
		}
		out[query] = path
	}
	return out, nil
}

// resultName is the base name CodeQL writes a query's results under.
func resultName(queryPath string) string {
	return strings.TrimSuffix(filepath.Base(queryPath), ".ql")
}

// collectBQRS indexes the results tree by query name. The pack directories above them vary with
// the pack, which is why the tree is walked rather than reconstructed.
func collectBQRS(results string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(results, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".bqrs" {
			return nil
		}
		out[strings.TrimSuffix(filepath.Base(path), ".bqrs")] = path
		return nil
	})
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading results: %w", err)
	}
	return out, nil
}
