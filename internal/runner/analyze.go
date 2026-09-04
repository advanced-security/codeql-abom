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

// DefaultSuite is the broadest upstream suite: every problem and path-problem query in
// codeql/actions-queries, excluding debug and model-generator helpers.
const DefaultSuite = "codeql/actions-queries:codeql-suites/actions-all.qls"

// AnalyzeDatabase evaluates a whole query suite against one database, writing SARIF.
//
// One analyze pass per database is far cheaper than running each query separately, and it is
// how ABOM gets upstream findings without forming any opinion about them.
func (r *Runner) AnalyzeDatabase(ctx context.Context, dbPath, suite, sarifPath string) error {
	if err := os.MkdirAll(filepath.Dir(sarifPath), 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	args := []string{
		"database", "analyze", dbPath, suite,
		"--format=sarif-latest",
		"--output=" + sarifPath,
		// Source snippets would multiply SARIF size across thousands of databases, and ABOM
		// locates findings by position rather than by content.
		"--no-sarif-add-file-contents",
	}
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("codeql database analyze %s: %w", suite, err)
		}
		return fmt.Errorf("codeql database analyze %s: %w: %s", suite, err, msg)
	}
	return nil
}
