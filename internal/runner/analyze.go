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

// DefaultSuite selects the upstream queries ABOM attributes findings to. It is a path into
// this repository, like defaultEdgesQuery, so it resolves only from the repository root;
// anywhere else, pass --suite.
const DefaultSuite = "ql/actions/abom/src/codeql-suites/abom-findings.qls"

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
		// The suite lives in a published pack, not in this repository. Without this the CLI
		// reports it as misspelled rather than as not yet fetched.
		"--download",
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
