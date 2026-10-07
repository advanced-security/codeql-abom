package catalog

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// The shape GitHub documents for GET /repos/{owner}/{repo}/git/trees/{tree_sha}?recursive=1.
const treeBody = `{
  "sha": "9fb037999f264ba9a7fc6274d15fa3ae2ab98312",
  "url": "https://api.github.com/repos/octo-org/app/git/trees/9fb037999f264ba9a7fc6274d15fa3ae2ab98312",
  "tree": [
    {"path": ".github", "mode": "040000", "type": "tree", "sha": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
    {"path": ".github/workflows/ci.yml", "mode": "100644", "type": "blob",
     "sha": "44b4fc6d56897b048c772eb4087f854f46256132", "size": 30},
    {"path": ".github/workflows/linked.yml", "mode": "120000", "type": "blob",
     "sha": "1f2e3d4c5b6a79881726354453627180fedcba98", "size": 6},
    {"path": "vendor/lib", "mode": "160000", "type": "commit",
     "sha": "0123456789abcdef0123456789abcdef01234567"}
  ],
  "truncated": %s
}`

// A commit's content is read from its tree, so the read has to be of that commit, and of the
// whole tree rather than its top level, where nothing an action manifest at depth would show.
func TestATreeIsListedRecursivelyAtTheCommitAsked(t *testing.T) {
	const commit = "aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/git/trees/"+commit; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.URL.Query().Get("recursive"); got != "1" {
			t.Errorf("recursive = %q, want 1", got)
		}
		fmt.Fprintf(w, treeBody, "false")
	})

	tree, err := c.Tree(context.Background(), "octo-org/app", commit)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if tree.Truncated {
		t.Error("an untruncated listing came back truncated")
	}
	want := []TreeEntry{
		{Path: ".github", Mode: "040000", Type: "tree", SHA: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		{Path: ".github/workflows/ci.yml", Mode: "100644", Type: "blob",
			SHA: "44b4fc6d56897b048c772eb4087f854f46256132"},
		{Path: ".github/workflows/linked.yml", Mode: "120000", Type: "blob",
			SHA: "1f2e3d4c5b6a79881726354453627180fedcba98"},
		{Path: "vendor/lib", Mode: "160000", Type: "commit",
			SHA: "0123456789abcdef0123456789abcdef01234567"},
	}
	if len(tree.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(tree.Entries), len(want), tree.Entries)
	}
	for i := range want {
		if tree.Entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, tree.Entries[i], want[i])
		}
	}
}

// GitHub stops listing a large tree and says so. Dropping the flag would present part of a tree
// as all of it, and two commits differing past the cut would read as identical.
func TestATruncatedTreeSaysSo(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, treeBody, "true")
	})
	tree, err := c.Tree(context.Background(), "octo-org/app", "aaaa")
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if !tree.Truncated {
		t.Error("a truncated listing came back whole")
	}
}

// A tree that could not be read is not an empty tree: an empty one would match nothing, but a
// failure reported as one would be indistinguishable from a repository with no content.
func TestATreeThatCannotBeReadIsAnError(t *testing.T) {
	for name, respond := range map[string]func(http.ResponseWriter){
		"missing": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message": "Not Found"}`)
		},
		"no tree in the answer": func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"sha": "aaaa", "truncated": false}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { respond(w) })
			if _, err := c.Tree(context.Background(), "octo-org/app", "aaaa"); err == nil {
				t.Error("an unreadable tree came back without an error")
			}
		})
	}
}
