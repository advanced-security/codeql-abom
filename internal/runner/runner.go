// Package runner extracts CodeQL databases and evaluates queries against them locally.
//
// Running locally is what frees ABOM from the GitHub MRVA backend's query-kind restrictions:
// the table query that produces the dependency graph has no `@kind`, so it yields BQRS but no
// SARIF, and only local evaluation guarantees the BQRS is available.
package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// databaseMarker is the file that identifies the root of an extracted CodeQL database.
const databaseMarker = "codeql-database.yml"

// Runner evaluates CodeQL queries with the CodeQL CLI.
type Runner struct {
	// CodeQLPath is the CLI to invoke. Empty means "codeql" from PATH.
	CodeQLPath string
	// Threads passed to the CLI; zero lets CodeQL choose.
	Threads int
	// RAMMB caps evaluator memory; zero lets CodeQL choose. Worth setting when many workers
	// share one machine, since the default sizes itself against total system memory.
	RAMMB int
	// Lock serializes the invocations that share CodeQL's compilation cache. Nil means none,
	// which is right for a one-shot command with nothing to contend with.
	Lock *Lock

	versionState
}

func (r *Runner) codeql() string {
	if r.CodeQLPath == "" {
		return "codeql"
	}
	return r.CodeQLPath
}

// RunQuery evaluates queryPath against the database at dbPath, writing BQRS to outputPath.
func (r *Runner) RunQuery(ctx context.Context, dbPath, queryPath, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	args := []string{
		"query", "run",
		"--database=" + dbPath,
		"--output=" + outputPath,
	}
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}
	args = append(args, queryPath)

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("codeql query run %s: %w", queryPath, err)
		}
		return fmt.Errorf("codeql query run %s: %w: %s", queryPath, err, msg)
	}
	return nil
}

// Extract unpacks a CodeQL database archive into destDir and returns the directory holding
// codeql-database.yml.
//
// Archive entries are validated against destDir before anything is written, because the
// archive is fetched from a remote API and a crafted entry name would otherwise be able to
// write anywhere the process can reach.
func Extract(zipPath, destDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", zipPath, err)
	}
	defer func() { _ = zr.Close() }()

	root, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("resolving destination: %w", err)
	}

	var markerDir string
	for _, entry := range zr.File {
		target, err := safeJoin(root, entry.Name)
		if err != nil {
			return "", err
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", fmt.Errorf("creating %s: %w", entry.Name, err)
			}
			continue
		}
		if err := writeEntry(entry, target); err != nil {
			return "", err
		}
		if filepath.Base(target) == databaseMarker {
			markerDir = filepath.Dir(target)
		}
	}

	if markerDir == "" {
		return "", fmt.Errorf("%s contains no %s", zipPath, databaseMarker)
	}
	return markerDir, nil
}

func writeEntry(entry *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", entry.Name, err)
	}

	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("opening %s: %w", entry.Name, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("creating %s: %w", entry.Name, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("extracting %s: %w", entry.Name, err)
	}
	return nil
}

// safeJoin resolves an archive entry name under root, rejecting anything that escapes it.
func safeJoin(root, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("archive entry %q is an absolute path", name)
	}

	target := filepath.Join(root, filepath.FromSlash(name))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination directory", name)
	}
	return target, nil
}
