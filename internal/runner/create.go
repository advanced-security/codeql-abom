package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoSourceCode reports that a source tree held nothing the extractor recognized.
//
// For an upstream dependency this is a permanent answer, not a transient failure: a repository
// with no workflows and no action manifest will still have none tomorrow.
var ErrNoSourceCode = errors.New("no source code seen during extraction")

// IsNoSourceCode reports whether err means the tree had nothing to extract.
func IsNoSourceCode(err error) bool { return errors.Is(err, ErrNoSourceCode) }

// CreateDatabase builds a CodeQL database for language from the tree at srcDir.
func (r *Runner) CreateDatabase(ctx context.Context, srcDir, dbDir, language string) error {
	return r.createDatabase(ctx, srcDir, dbDir, language, nil)
}

// CreateDatabaseIndexing builds a database that also indexes paths the extractor's defaults
// leave out, appended after the defaults and after any filters the environment already sets.
func (r *Runner) CreateDatabaseIndexing(
	ctx context.Context, srcDir, dbDir, language string, includes []string,
) error {
	if r.CodeScanningConfig != "" && len(includes) > 0 {
		return fmt.Errorf("codeql database create %s: a code scanning configuration rewrites "+
			"the index filters, so it cannot be combined with %d appended includes",
			srcDir, len(includes))
	}
	return r.createDatabase(ctx, srcDir, dbDir, language, includes)
}

func (r *Runner) createDatabase(
	ctx context.Context, srcDir, dbDir, language string, includes []string,
) error {
	// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
	if err := os.RemoveAll(dbDir); err != nil {
		return fmt.Errorf("clearing %s: %w", dbDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(dbDir), 0o755); err != nil {
		return fmt.Errorf("creating database parent: %w", err)
	}

	args := []string{
		"database", "create",
		"--language=" + language,
		"--source-root=" + srcDir,
		"--overwrite",
	}
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}
	if r.CodeScanningConfig != "" {
		args = append(args, "--codescanning-config="+r.CodeScanningConfig)
	}
	args = append(args, dbDir)

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	if len(includes) > 0 {
		cmd.Env = append(os.Environ(), "LGTM_INDEX_FILTERS="+indexFilters(includes))
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if isNoSourceMessage(msg) {
			return fmt.Errorf("%s: %w", srcDir, ErrNoSourceCode)
		}
		if msg == "" {
			return fmt.Errorf("codeql database create %s: %w", srcDir, err)
		}
		return fmt.Errorf("codeql database create %s: %w: %s", srcDir, err, msg)
	}
	return nil
}

// indexFilters is the value the extractor reads: whatever the environment already set, then one
// include per pattern. The extractor's autobuild places all of it after its own defaults.
func indexFilters(includes []string) string {
	lines := make([]string, 0, len(includes)+1)
	if inherited := os.Getenv("LGTM_INDEX_FILTERS"); inherited != "" {
		lines = append(lines, inherited)
	}
	for _, pattern := range includes {
		lines = append(lines, "include:"+pattern)
	}
	return strings.Join(lines, "\n")
}

func isNoSourceMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "no source code was seen") ||
		strings.Contains(lower, "no code found")
}
