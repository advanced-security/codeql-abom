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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
)

// ErrUnavailable reports a permanent answer for a dependency: the repository cannot be read, or
// holds nothing an actions database can be built from. Retrying spends quota to no purpose.
var ErrUnavailable = errors.New("dependency unavailable")

// IsUnavailable reports whether err is a permanent answer.
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || sourcepolicy.IsDenied(err)
}

// NothingToAnalyze is a tree read in full that holds no workflow and no action manifest, with the
// commit it was read at, empty when the repository has no commit. Unavailable for a dependency,
// which cannot run; for a repository somebody owns it is a reading, settled at that commit.
type NothingToAnalyze struct {
	Tree Tree
	Why  string
}

func (e *NothingToAnalyze) Error() string { return ErrUnavailable.Error() + ": " + e.Why }

func (e *NothingToAnalyze) Unwrap() error { return ErrUnavailable }

// ErrReferenceMissing is a repository answering that it holds no such reference, which a command
// that failed cannot say. It always comes with ErrUnavailable.
var ErrReferenceMissing = errors.New("reference missing")

// IsReferenceMissing reports whether err is a repository answering that a reference does not exist.
func IsReferenceMissing(err error) bool { return errors.Is(err, ErrReferenceMissing) }

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
	// Tokens authenticates every git call as the owner's installation, or the lent one. Nil runs git as before an App.
	Tokens       ghapp.TokenSource
	SourcePolicy func(context.Context, string) error
}

// gitIdentity is the credential every git call under one fetch or probe carries, and the owner it was lent for.
type gitIdentity struct {
	auth ghapp.GitAuth
	lent string
}

type gitIdentityKey struct{}

// as carries owner's credential to git calls under ctx; one that cannot be had is retryable, never "unavailable".
func (c *Checkout) as(ctx context.Context, owner string) (context.Context, error) {
	if c.Tokens == nil {
		return ctx, nil
	}
	token, lent, err := c.Tokens.Token(ctx, owner)
	if err != nil {
		return ctx, fmt.Errorf("choosing a credential to read %s: %w", owner, err)
	}
	id := gitIdentity{auth: ghapp.GitCredentials(c.host(), token)}
	if lent {
		id.lent = owner
	}
	return context.WithValue(ctx, gitIdentityKey{}, id), nil
}

// host is the scheme and authority the credential is scoped to, so it reaches no other host.
func (c *Checkout) host() string {
	base := c.BaseURL
	if base == "" {
		base = "https://github.com"
	}
	if u, err := url.Parse(base); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return strings.TrimSuffix(base, "/")
}

// unavailable is ErrUnavailable, and ErrNotInstalled where a lent token was refused: a private repository needs the App.
func unavailable(ctx context.Context, format string, args ...any) error {
	err := fmt.Errorf("%w: "+format, append([]any{ErrUnavailable}, args...)...)
	if id, _ := ctx.Value(gitIdentityKey{}).(gitIdentity); id.lent != "" {
		return fmt.Errorf("%w; %w: %s, so a private repository there cannot be read", err, ghapp.ErrNotInstalled, id.lent)
	}
	return err
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
	if c.SourcePolicy != nil {
		if err := c.SourcePolicy(ctx, owner+"/"+repo); err != nil {
			return Tree{}, err
		}
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	ctx, err := c.as(ctx, owner)
	if err != nil {
		return Tree{}, err
	}
	if err := prepareCheckoutDir(dest); err != nil {
		return Tree{}, err
	}

	url := c.cloneURL(owner, repo)
	if out, err := c.clone(ctx, dest, url, ref); err != nil {
		return Tree{}, unavailable(ctx, "cloning %s at %q: %s", url, ref, out)
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
		return Tree{}, &NothingToAnalyze{Tree: Tree{Ref: tree.Ref, SHA: tree.SHA},
			Why: repo + " has no workflows or action manifest"}
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
			return unavailable(ctx, "fetching %s of %s: %s", ref, repo, out)
		}
		if out, err := c.run(ctx, dest, "checkout", ref); err != nil {
			return fmt.Errorf("%w: checking out %s of %s: %s",
				ErrUnavailable, ref, repo, out)
		}
		return nil
	}
	if out, err := c.run(ctx, dest, "checkout"); err != nil {
		if unbornBranch(out) {
			return &NothingToAnalyze{Tree: Tree{Ref: analyzedRef(ref)},
				Why: fmt.Sprintf("%s has no commit on its default branch: %s", repo, out)}
		}
		return fmt.Errorf("checking out %s: %s", repo, out)
	}
	return nil
}

// unbornBranch reports git saying the default branch has no commit: an empty repository's clone.
func unbornBranch(out string) bool {
	return strings.Contains(out, "yet to be born") ||
		strings.Contains(out, "does not have any commits yet")
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
	id, _ := ctx.Value(gitIdentityKey{}).(gitIdentity)
	cmd := exec.CommandContext(ctx, c.git(), append(slices.Clip(id.auth.Args), args...)...)
	cmd.Dir = dir
	// Nothing here may prompt: expansion runs unattended and a credential prompt would hang it.
	// An empty GIT_ASKPASS also overrides core.askPass and SSH_ASKPASS for git's own prompts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")
	cmd.Env = append(cmd.Env, id.auth.Env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(stderr.String()), err
	}
	return stdout.String(), nil
}
