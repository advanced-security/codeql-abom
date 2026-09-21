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
//
// Measured on the corpus before this was fixed: 5 of 12 sampled symbolic references resolved to a
// tag object, including `actions-rs/toolchain@v1` and `1password/load-secrets-action@v2`.
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
