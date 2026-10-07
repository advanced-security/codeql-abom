package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// TreeEntry is one path of a commit's tree, as GitHub lists it.
type TreeEntry struct {
	Path string
	Mode string
	// Type is `blob`, `tree`, or `commit` for a submodule.
	Type string
	SHA  string
}

// Tree is a commit's tree, listed recursively.
type Tree struct {
	Entries []TreeEntry
	// Truncated is GitHub saying it stopped listing before the end of the tree.
	Truncated bool
}

// Tree lists a commit's whole tree in one read, without fetching any of its content.
//
// Object ids are hashes of content, so what two commits hold can be compared by this one request
// where it would otherwise take a clone of each.
func (c *Client) Tree(ctx context.Context, repository, commit string) (Tree, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return Tree{}, err
	}
	if commit == "" {
		return Tree{}, fmt.Errorf("listing a tree of %s: no commit named", repository)
	}
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name), url.PathEscape(commit)), jsonAccept)
	if err != nil {
		return Tree{}, err
	}
	defer resp.Body.Close()

	var out struct {
		// A pointer, so an answer naming no tree is told apart from an empty one.
		Tree *[]struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Tree{}, fmt.Errorf("decoding the tree of %s at %s: %w", repository, commit, err)
	}
	if out.Tree == nil {
		return Tree{}, fmt.Errorf("the tree of %s at %s came back without one", repository, commit)
	}
	tree := Tree{Entries: make([]TreeEntry, 0, len(*out.Tree)), Truncated: out.Truncated}
	for _, e := range *out.Tree {
		tree.Entries = append(tree.Entries, TreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.SHA})
	}
	return tree, nil
}
