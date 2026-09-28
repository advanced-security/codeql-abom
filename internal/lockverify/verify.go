// Package lockverify runs GitHub's own lock verification on request, and says what it found
// without turning any of it into a verdict.
//
// The pinned `gh actions-lock` owns verification. This runs it only no-fix, only on a copy of the
// tree, and only when somebody asks: never in a getter, never on a render. What the tool reports
// is kept as the tool reported it. A category this build does not know, a check the tool could
// not decide and a tool that failed are each their own outcome, and none of them is a pass.
package lockverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Modes a verification runs in.
const (
	// ModeLocal is the offline coverage check: every `uses:` has an entry. No network, no token.
	ModeLocal = "local"
	// ModeNetwork re-verifies every pin against GitHub: that it still resolves and is reachable.
	ModeNetwork = "network"
)

// Outcomes, none of which is a statement that a workflow is safe.
const (
	// OutcomePassed is the tool's check passing with nothing to report.
	OutcomePassed = "passed"
	// OutcomeWarnings is the check passing with findings the tool rated below an error.
	OutcomeWarnings = "warnings"
	// OutcomeFailed is the tool calling the lock invalid.
	OutcomeFailed = "failed"
	// OutcomeInconclusive is a report this build cannot read in full, or one where the tool
	// could not decide: a category it does not know, or reachability it could not confirm.
	OutcomeInconclusive = "inconclusive"
	// OutcomeError is the tool failing to answer at all, or answering after changing its inputs.
	OutcomeError = "error"
)

// knownCategories are the categories the pinned CLI (v0.1.6) reports. One outside this set is
// kept verbatim and makes the outcome inconclusive, because nobody here knows what it claims.
var knownCategories = map[string]bool{
	"not-pinned": true, "sha-as-ref": true, "ref-changed": true, "ref-moved": true, "stale": true,
	"misleading-sha": true, "unreachable-pin": true, "valid": true, "run-only": true,
	"ancestry-unknown": true, "reachability-unknown": true, "onboarding-required": true,
	"version-ref": true, "local-action": true, "stale-workflow": true,
	"self-repository-action": true, "invalid-self-repository-ref": true,
}

// undecidedCategories are the tool saying it could not tell, which is not a pass.
var undecidedCategories = map[string]bool{"ancestry-unknown": true, "reachability-unknown": true}

// Finding is one diagnostic, as the tool wrote it.
type Finding struct {
	Workflow   string `json:"workflow"`
	Category   string `json:"category"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence,omitempty"`
	Dependency string `json:"dependency,omitempty"`
	RequiredBy string `json:"requiredBy,omitempty"`
	Detail     string `json:"detail"`
	// Known is false for a category this build does not recognize.
	Known bool `json:"known"`
}

// Result is one run of the tool.
type Result struct {
	Mode            string    `json:"mode"`
	Outcome         string    `json:"outcome"`
	ToolVersion     string    `json:"toolVersion"`
	LockfileVersion string    `json:"lockfileVersion"`
	Valid           *bool     `json:"valid"`
	Findings        []Finding `json:"findings"`
	// Error is why there is no answer, empty when the tool gave one.
	Error string `json:"error,omitempty"`
}

// Runner calls the tool.
type Runner struct {
	// Gh is the gh binary; empty means "gh" from PATH.
	Gh string
	// Git is the git binary the copy is committed with; empty means "git" from PATH.
	Git     string
	Timeout time.Duration
}

// report is the tool's JSON, the fields asked for.
type report struct {
	CLIVersion      string `json:"cli_version"`
	LockfileVersion string `json:"lockfile_version"`
	Valid           *bool  `json:"valid"`
	Findings        []struct {
		Workflow   string `json:"workflow"`
		Category   string `json:"category"`
		Severity   string `json:"severity"`
		Confidence string `json:"confidence"`
		Dependency string `json:"dependency"`
		RequiredBy string `json:"required_by"`
		Detail     string `json:"detail"`
	} `json:"findings"`
}

// Verify runs the tool against a copy of tree in mode.
//
// A copy, because the trees handed here are cached checkouts other work reads, and a tool that
// wrote to one would change what a later analysis sees. A repository of its own at one commit,
// because the tool reads `$/` actions through git and reports every one as unreadable otherwise.
func (r Runner) Verify(ctx context.Context, tree, mode string) Result {
	out := Result{Mode: mode, Findings: []Finding{}}
	flag := map[string]string{ModeLocal: "--verify-local", ModeNetwork: "--verify"}[mode]
	if flag == "" {
		out.Outcome, out.Error = OutcomeError, fmt.Sprintf("no verification mode %q", mode)
		return out
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	work, err := os.MkdirTemp("", "abom-lockverify-*")
	if err != nil {
		out.Outcome, out.Error = OutcomeError, err.Error()
		return out
	}
	defer os.RemoveAll(work)
	if err := copyTree(tree, work); err != nil {
		out.Outcome, out.Error = OutcomeError, "copying the tree: "+err.Error()
		return out
	}
	if err := r.commit(ctx, work); err != nil {
		out.Outcome, out.Error = OutcomeError, "making the copy a repository: "+err.Error()
		return out
	}
	before, err := digestTree(work)
	if err != nil {
		out.Outcome, out.Error = OutcomeError, err.Error()
		return out
	}

	gh := r.Gh
	if gh == "" {
		gh = "gh"
	}
	cmd := exec.CommandContext(ctx, gh, "actions-lock", flag, "--no-fix", "--no-interactive",
		"--json=valid,findings")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	after, err := digestTree(work)
	if err != nil {
		out.Outcome, out.Error = OutcomeError, err.Error()
		return out
	}
	if after != before {
		out.Outcome = OutcomeError
		out.Error = "the tool rewrote its inputs under --no-fix, so nothing it said is recorded"
		return out
	}

	var rep report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || rep.CLIVersion == "" {
		out.Outcome, out.Error = OutcomeError, failureMessage(runErr, stderr.String())
		return out
	}
	out.ToolVersion, out.LockfileVersion, out.Valid = rep.CLIVersion, rep.LockfileVersion, rep.Valid
	unknown, undecided := false, false
	for _, f := range rep.Findings {
		known := knownCategories[f.Category]
		unknown = unknown || !known
		undecided = undecided || undecidedCategories[f.Category]
		out.Findings = append(out.Findings, Finding{
			Workflow: f.Workflow, Category: f.Category, Severity: f.Severity,
			Confidence: f.Confidence, Dependency: f.Dependency, RequiredBy: f.RequiredBy,
			Detail: f.Detail, Known: known,
		})
	}
	switch {
	case rep.Valid == nil:
		out.Outcome, out.Error = OutcomeError, "the tool's report says nothing about validity"
	case unknown || undecided:
		out.Outcome = OutcomeInconclusive
	case !*rep.Valid:
		out.Outcome = OutcomeFailed
	case len(out.Findings) > 0:
		out.Outcome = OutcomeWarnings
	default:
		out.Outcome = OutcomePassed
	}
	return out
}

// failureMessage names why the tool gave no report: its own words when it left any.
func failureMessage(runErr error, stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			return fmt.Sprintf("the tool exited %d without a report", exit.ExitCode())
		}
		return runErr.Error()
	}
	return "the tool wrote no report"
}

// commit makes dir a repository with everything in it committed, under a fixed identity.
func (r Runner) commit(ctx context.Context, dir string) error {
	git := r.Git
	if git == "" {
		git = "git"
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=abom", "-c", "user.email=abom@localhost", "-c", "commit.gpgsign=false",
			"commit", "-q", "--allow-empty", "-m", "tree under verification"},
	} {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", args[len(args)-1], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// copyTree copies the regular files of src into dst, leaving out `.git` and anything that is not
// a regular file: a symlink in a tree downloaded from a stranger is an instruction to read
// somewhere else.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// digestTree hashes every file outside `.git`, path and content, so any write is visible.
func digestTree(dir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("reading the copy: %w", err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(h, "%d:%s\x00", len(rel), filepath.ToSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading the copy: %w", err)
		}
		sum := sha256.Sum256(body)
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
