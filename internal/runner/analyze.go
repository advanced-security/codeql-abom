package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultSuite selects the upstream queries ABOM attributes findings to. It names a pack rather
// than a path, so it resolves the same whether the pack is in this repository or installed.
const DefaultSuite = "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"

// AnalyzeDatabase evaluates a whole query suite against one database, writing SARIF.
//
// One analyze pass per database is far cheaper than running each query separately, and it is
// how ABOM gets upstream findings without forming any opinion about them.
func (r *Runner) AnalyzeDatabase(ctx context.Context, dbPath, suite, sarifPath string) error {
	return r.AnalyzeQueries(ctx, dbPath, []string{suite}, sarifPath)
}

// AnalyzeQueries evaluates named queries or suites against one database, writing SARIF.
//
// Naming queries individually is what lets a suite hold both lanes: `database analyze` cannot
// run a `@kind graph` query, so the expander passes only the findings-shaped members rather
// than the suite that selected them.
func (r *Runner) AnalyzeQueries(
	ctx context.Context, dbPath string, targets []string, sarifPath string,
) error {
	if len(targets) == 0 {
		return fmt.Errorf("runner: analyze needs at least one query or suite")
	}
	if err := os.MkdirAll(filepath.Dir(sarifPath), 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	args := append([]string{"database", "analyze", dbPath}, targets...)
	args = append(args, localPackArgs()...)
	args = append(args,
		"--format=sarif-latest",
		"--output="+sarifPath,
		// The suite lives in a published pack, not in this repository. Without this the CLI
		// reports it as misspelled rather than as not yet fetched.
		"--download",
		// Source snippets would multiply SARIF size across thousands of databases, and ABOM
		// locates findings by position rather than by content.
		"--no-sarif-add-file-contents",
	)
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	named := strings.Join(targets, " ")
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("codeql database analyze %s: %w", named, err)
		}
		return fmt.Errorf("codeql database analyze %s: %w: %s", named, err, msg)
	}
	return nil
}
