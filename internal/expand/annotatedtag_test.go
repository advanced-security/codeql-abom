package expand

import (
	"context"
	"path/filepath"
	"testing"
)

// An annotated tag has its own object, and `ls-remote` reports that object rather than the commit
// it points at. A probe answering with it compares a tag id against the commit a checkout
// produced, so the reference reads as moved every single pass: re-analyzed, stored as the same
// commit, and due again immediately. That is the treadmill Resolve's own documentation warns
// about, reached through the tag rather than through the default branch.
func TestResolveReportsTheCommitOfAnAnnotatedTagRatherThanItsTagObject(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	commit := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "tag", "-a", "v1", "-m", "release v1")

	tagObject := gitIn(t, origin, "rev-parse", "v1")
	if tagObject == commit {
		t.Fatal("the tag is not annotated, so this proves nothing")
	}

	got, err := newCheckout(t, origin).Resolve(context.Background(), "", filepath.Base(origin), "v1")
	if err != nil {
		t.Fatalf("resolving an annotated tag: %v", err)
	}
	if got == tagObject {
		t.Fatalf("resolved to the tag object %s; the commit is %s", got[:12], commit[:12])
	}
	if got != commit {
		t.Fatalf("resolved to %s, want the commit %s", got, commit)
	}
}

// A lightweight tag has no separate object, and must keep resolving to the commit it names.
func TestResolveStillReportsTheCommitOfALightweightTag(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	commit := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "tag", "v1")

	got, err := newCheckout(t, origin).Resolve(context.Background(), "", filepath.Base(origin), "v1")
	if err != nil {
		t.Fatalf("resolving a lightweight tag: %v", err)
	}
	if got != commit {
		t.Fatalf("resolved to %s, want %s", got, commit)
	}
}

// `ls-remote` matches a pattern against the end of every ref, so `v1` also matches
// `refs/heads/releases/v1`, and taking the first line answered with that branch: the sweep read
// actions/setup-node@v1 as moved on every pass. A reference names one ref exactly.
func TestResolveReadsTheTagNamedNotABranchEndingInTheSameName(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	tagged := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "tag", "v1")
	gitIn(t, origin, "checkout", "-q", "-b", "releases/v1")
	addCommit(t, origin, "action.yml", "name: release branch\n")
	gitIn(t, origin, "checkout", "-q", "-")

	got, err := newCheckout(t, origin).Resolve(context.Background(), "", filepath.Base(origin), "v1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != tagged {
		t.Errorf("Resolve(v1) = %s, want the tag's commit %s, not releases/v1", got, tagged)
	}
}

// The webhook processor probes an owned repository's branch by name; `main` is not `feature/main`.
func TestResolveReadsTheBranchNamedNotOneEndingInTheSameName(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	branch := gitIn(t, origin, "rev-parse", "--abbrev-ref", "HEAD")
	want := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "checkout", "-q", "-b", "feature/"+branch)
	addCommit(t, origin, "action.yml", "name: feature\n")
	gitIn(t, origin, "checkout", "-q", branch)

	got, err := newCheckout(t, origin).Resolve(context.Background(), "", filepath.Base(origin), branch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve(%s) = %s, want %s, not feature/%s", branch, got, want, branch)
	}
}

// A name that is both a branch and a tag is the branch, as `git clone --branch` reads it, so a
// probe and the checkout it decides about agree.
func TestResolvePrefersTheBranchWhereANameIsBothAsCloneDoes(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	gitIn(t, origin, "tag", "v2")
	gitIn(t, origin, "checkout", "-q", "-b", "v2")
	addCommit(t, origin, "action.yml", "name: branch v2\n")
	branchHead := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "checkout", "-q", "-")

	got, err := newCheckout(t, origin).Resolve(context.Background(), "", filepath.Base(origin), "v2")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != branchHead {
		t.Errorf("Resolve(v2) = %s, want the branch v2 at %s", got, branchHead)
	}
}

// A reference the repository does not hold is a fact about the reference; a command that failed is
// not, and an event acting on the second would mark live references gone on a network error.
func TestResolveTellsAMissingReferenceFromARepositoryItCouldNotRead(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: probe\n"})
	c := newCheckout(t, origin)

	_, missing := c.Resolve(context.Background(), "", filepath.Base(origin), "v9")
	if !IsUnavailable(missing) || !IsReferenceMissing(missing) {
		t.Errorf("a tag the repository does not hold reads as %v, want unavailable and missing", missing)
	}
	_, unreadable := c.Resolve(context.Background(), "", "no-such-repository", "v1")
	if !IsUnavailable(unreadable) || IsReferenceMissing(unreadable) {
		t.Errorf("a repository that could not be read reads as %v, want unavailable and not missing", unreadable)
	}
}
