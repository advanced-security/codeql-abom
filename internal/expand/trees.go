package expand

import (
	"context"

	"github.com/advanced-security/codeql-abom/internal/catalog"
)

// TreeReader lists a commit's whole tree without fetching its content.
type TreeReader interface {
	ReadTree(ctx context.Context, owner, repo, commit string) (Listing, error)
}

// GitHubTrees reads trees through GitHub's API.
type GitHubTrees struct {
	Client *catalog.Client
}

// ReadTree lists one commit's tree in a single request.
func (g GitHubTrees) ReadTree(ctx context.Context, owner, repo, commit string) (Listing, error) {
	tree, err := g.Client.Tree(ctx, owner+"/"+repo, commit)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Entries: make([]ListedEntry, 0, len(tree.Entries)), Truncated: tree.Truncated}
	for _, e := range tree.Entries {
		l.Entries = append(l.Entries, ListedEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.SHA})
	}
	return l, nil
}
