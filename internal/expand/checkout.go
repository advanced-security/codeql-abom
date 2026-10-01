// Package expand resolves upstream dependencies into graph data.
//
// Analyzing a dependency means fetching enough of its source to build an actions database. That
// is a tiny fraction of a typical repository, so the checkout is filtered and sparse: at
// organization scale the difference between cloning whole repositories and cloning only their
// actions content is the difference between a feasible expansion and an infeasible one.
package expand

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnavailable reports a permanent answer for a dependency: the repository cannot be read, or
// holds nothing an actions database can be built from. Retrying spends quota to no purpose.
var ErrUnavailable = errors.New("dependency unavailable")

// IsUnavailable reports whether err is a permanent answer.
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }

// sparsePatterns are the only paths an actions database is built from.
//
// Unanchored patterns match at any depth, which picks up composite actions in subdirectories
// along with the manifests they reference by relative path.
var sparsePatterns = []string{"/.github/", "action.yml", "action.yaml"}

// Tree is a materialized source tree and the commit it came from.
type Tree struct {
	Dir string
	// Ref is what was requested; SHA is what it resolved to.
	Ref string
	SHA string
}

// Checkout materializes the minimum tree needed for an actions database.
type Checkout struct {
	// Git is the binary to invoke; empty means "git" from PATH.
	Git string
	// BaseURL is the host to clone from; empty means github.com. Overridable for testing.
	BaseURL string
	Timeout time.Duration
}

func (c *Checkout) git() string {
	if c.Git == "" {
		return "git"
	}
	return c.Git
}

// SparsePatterns states the filter this fetch applies, so a cached tree is only reused for a
// request under the same one. Widening the set here makes every held tree a miss, which is the
// correct outcome: none of them contain the paths that were added.
func (c *Checkout) SparsePatterns() []string { return sparsePatterns }

func (c *Checkout) cloneURL(owner, repo string) string {
	base := c.BaseURL
	if base == "" {
		base = "https://github.com"
	}
	base = strings.TrimSuffix(base, "/")
	if owner != "" {
		base += "/" + owner
	}
	url := base + "/" + repo
	if strings.HasPrefix(url, "http") {
		url += ".git"
	}
	return url
}

// Fetch materializes owner/repo at ref into dest. An empty ref means the default branch.
func (c *Checkout) Fetch(ctx context.Context, dest, owner, repo, ref string) (Tree, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	if err := prepareCheckoutDir(dest); err != nil {
		return Tree{}, err
	}

	url := c.cloneURL(owner, repo)
	if out, err := c.clone(ctx, dest, url, ref); err != nil {
		return Tree{}, fmt.Errorf("%w: cloning %s at %q: %s", ErrUnavailable, url, ref, out)
	}
	if out, err := c.sparseCheckout(ctx, dest); err != nil {
		return Tree{}, fmt.Errorf("selecting actions content in %s: %s", repo, out)
	}
	if err := c.checkout(ctx, dest, repo, ref); err != nil {
		return Tree{}, err
	}

	sha, err := c.run(ctx, dest, "rev-parse", "HEAD")
	if err != nil {
		return Tree{}, fmt.Errorf("resolving HEAD of %s: %s", repo, sha)
	}

	// The ref is recorded as asked for, not as "HEAD": a row for `v4` that reported HEAD could
	// never be told apart from one analyzed at the default branch.
	tree := Tree{Dir: dest, Ref: analyzedRef(ref), SHA: strings.TrimSpace(sha)}
	if !hasActionsContent(dest) {
		return Tree{}, fmt.Errorf("%w: %s has no workflows or action manifest", ErrUnavailable, repo)
	}
	return tree, nil
}

func prepareCheckoutDir(dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clearing %s: %w", dest, err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	return nil
}

func (c *Checkout) clone(ctx context.Context, dest, url, ref string) (string, error) {
	// blob:none defers file downloads until sparse-checkout has decided what is wanted, so
	// only the matching blobs ever cross the network.
	clone := []string{"clone", "--filter=blob:none", "--no-checkout", "--depth=1"}
	// A tag or branch is fetched by name at depth 1. A commit cannot be: `clone --branch` only
	// accepts refs, so a SHA-pinned use is fetched below instead.
	if ref != "" && !isCommit(ref) {
		clone = append(clone, "--branch", ref)
	}
	clone = append(clone, url, ".")
	return c.run(ctx, dest, clone...)
}

func (c *Checkout) sparseCheckout(ctx context.Context, dest string) (string, error) {
	args := append([]string{"sparse-checkout", "set", "--no-cone"}, sparsePatterns...)
	return c.run(ctx, dest, args...)
}

func (c *Checkout) checkout(ctx context.Context, dest, repo, ref string) error {
	if ref != "" && isCommit(ref) {
		if out, err := c.run(ctx, dest, "fetch", "--depth=1", "origin", ref); err != nil {
			return fmt.Errorf("%w: fetching %s of %s: %s", ErrUnavailable, ref, repo, out)
		}
		if out, err := c.run(ctx, dest, "checkout", ref); err != nil {
			return fmt.Errorf("%w: checking out %s of %s: %s",
				ErrUnavailable, ref, repo, out)
		}
		return nil
	}
	if out, err := c.run(ctx, dest, "checkout"); err != nil {
		return fmt.Errorf("checking out %s: %s", repo, out)
	}
	return nil
}

// hasActionsContent reports whether anything survived the sparse filter.
func hasActionsContent(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".github")); err == nil {
		return true
	}
	found := false
	// The walk's own error is deliberately discarded: an unreadable tree means nothing was
	// found, which is exactly what the zero value says.
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not actions content. Skipping it is the answer; abandoning
			// the walk would report the rest of the tree as empty.
			return nil //nolint:nilerr
		}
		if found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "action.yml" || d.Name() == "action.yaml" {
			found = true
		}
		return nil
	})
	return found
}

func (c *Checkout) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.git(), args...)
	cmd.Dir = dir
	// Nothing here may prompt: expansion runs unattended and a credential prompt would hang it.
	// An empty GIT_ASKPASS also overrides core.askPass and SSH_ASKPASS for git's own prompts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(stderr.String()), err
	}
	return stdout.String(), nil
}
