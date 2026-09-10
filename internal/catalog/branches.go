package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ProtectedBranches names the branches a repository protects.
//
// Whether a branch is protected decides whether a pull request check can *block* a merge or only
// comment on it, so a gate that cannot ask this cannot say what it is able to do.
//
// An empty result is a finding — this repository protects nothing. It is not the same statement as
// never having asked, which is why the caller records that it looked; see the store side.
//
// `?protected=true` filters server-side rather than listing every branch and discarding most of
// them, because a repository with hundreds of branches would otherwise cost pages of requests to
// answer a question about two of them.
func (c *Client) ProtectedBranches(ctx context.Context, repository string) ([]string, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("repository %q is not owner/name", repository)
	}

	next := fmt.Sprintf("%s/repos/%s/%s/branches?protected=true&per_page=%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), reposPerPage)

	branches := []string{}
	for next != "" {
		resp, err := c.do(ctx, next, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}

		var page []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding branches for %s: %w", repository, err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()

		for _, b := range page {
			if b.Name == "" {
				continue
			}
			branches = append(branches, b.Name)
		}
		next = nextLink(link)
	}
	return branches, nil
}
