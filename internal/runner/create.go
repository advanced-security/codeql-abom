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

// ErrNoSourceCode reports that a source tree held nothing the extractor recognised.
//
// For an upstream dependency this is a permanent answer, not a transient failure: a repository
// with no workflows and no action manifest will still have none tomorrow.
var ErrNoSourceCode = errors.New("no source code seen during extraction")

// IsNoSourceCode reports whether err means the tree had nothing to extract.
func IsNoSourceCode(err error) bool { return errors.Is(err, ErrNoSourceCode) }

// CreateDatabase builds a CodeQL database for language from the tree at srcDir.
func (r *Runner) CreateDatabase(ctx context.Context, srcDir, dbDir, language string) error {
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
	args = append(args, dbDir)

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
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

func isNoSourceMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "no source code was seen") ||
		strings.Contains(lower, "no code found")
}
