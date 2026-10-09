package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	var env []string
	if len(includes) > 0 {
		env = append(os.Environ(), "LGTM_INDEX_FILTERS="+indexFilters(includes))
	}
	for _, args := range r.createSteps(srcDir, dbDir, language) {
		if err := r.runCreateStep(ctx, srcDir, args, env); err != nil {
			return err
		}
	}
	return nil
}

// untracedAutobuild names the languages whose extractor has no build mode none and whose
// autobuilder extracts the source itself, which the extractor's tracing configuration leaves
// untraced anyway.
var untracedAutobuild = map[string]bool{"go": true}

// createSteps is the CLI invocations that build a database without tracing a build. Tracing
// relaunches the build through a copy of a system shell, which an arm64 Mac without Rosetta cannot
// start, and nothing ABOM analyzes needs a traced build to be read.
func (r *Runner) createSteps(srcDir, dbDir, language string) [][]string {
	var limits, config []string
	if r.Threads > 0 {
		limits = append(limits, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		limits = append(limits, fmt.Sprintf("--ram=%d", r.RAMMB))
	}
	if r.CodeScanningConfig != "" {
		config = []string{"--codescanning-config=" + r.CodeScanningConfig}
	}
	source := []string{"--language=" + language, "--source-root=" + srcDir}

	if untracedAutobuild[language] {
		return [][]string{
			slices.Concat([]string{"database", "init"}, source,
				[]string{"--build-mode=autobuild", "--overwrite"}, config, []string{dbDir}),
			slices.Concat([]string{"database", "trace-command", "--no-tracing", "--use-build-mode"},
				limits, []string{dbDir}),
			slices.Concat([]string{"database", "finalize"}, limits, []string{dbDir}),
		}
	}
	return [][]string{slices.Concat([]string{"database", "create"}, source,
		[]string{"--build-mode=none", "--overwrite"}, limits, config, []string{dbDir})}
}

// runCreateStep runs one invocation, telling a tree with nothing to extract from a failure.
func (r *Runner) runCreateStep(ctx context.Context, srcDir string, args, env []string) error {
	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if isNoSourceMessage(msg) {
			return fmt.Errorf("%s: %w", srcDir, ErrNoSourceCode)
		}
		step := "codeql " + strings.Join(args[:2], " ")
		if msg == "" {
			return fmt.Errorf("%s %s: %w", step, srcDir, err)
		}
		return fmt.Errorf("%s %s: %w: %s", step, srcDir, err, msg)
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
