package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// DependencyChange is one dependency a comparison found added or removed, as GitHub's dependency
// graph names it.
type DependencyChange struct {
	ChangeType string `json:"change_type"`
	Manifest   string `json:"manifest"`
	Ecosystem  string `json:"ecosystem"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

// DependencyComparison is what dependency review says changed between two commits.
type DependencyComparison struct {
	Changes []DependencyChange
	// Warnings is GitHub saying the comparison is incomplete, such as a snapshot missing for one of
	// the commits; empty when it said nothing.
	Warnings string
}

// Actions is the comparison's GitHub Actions dependencies: what workflows name in `uses:`.
func (d DependencyComparison) Actions() []DependencyChange {
	var out []DependencyChange
	for _, c := range d.Changes {
		if strings.EqualFold(c.Ecosystem, "actions") {
			out = append(out, c)
		}
	}
	return out
}

// snapshotWarnings is the header GitHub sends a comparison's warnings in, base64 encoded.
const snapshotWarnings = "X-GitHub-Dependency-Graph-Snapshot-Warnings"

// CompareDependencies asks dependency review what changed between two commits of a repository.
//
// It reads GitHub's dependency graph, which parses workflows and not a composite action's
// action.yml, so a comparison naming nothing is not evidence that no `uses:` moved.
func (c *Client) CompareDependencies(ctx context.Context, repository, base, head string) (DependencyComparison, error) {
	var out DependencyComparison
	owner, name, err := splitRepository(repository)
	if err != nil {
		return out, err
	}
	if base == "" || head == "" {
		return out, fmt.Errorf("comparing the dependencies of %s: both commits are needed", repository)
	}
	next := fmt.Sprintf("%s/repos/%s/%s/dependency-graph/compare/%s...%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name), url.PathEscape(base), url.PathEscape(head))
	for next != "" {
		resp, err := c.doUncached(ctx, next, jsonAccept)
		if err != nil {
			return out, err
		}
		var page []DependencyChange
		err = json.NewDecoder(resp.Body).Decode(&page)
		warned := resp.Header.Get(snapshotWarnings)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return out, fmt.Errorf("decoding the dependency comparison of %s: %w", repository, err)
		}
		out.Changes = append(out.Changes, page...)
		if w := decodeWarnings(warned); w != "" {
			out.Warnings = w
		}
		next = nextLink(link)
	}
	return out, nil
}

// decodeWarnings reads the warnings header, keeping a value that is not base64 as GitHub sent it.
func decodeWarnings(header string) string {
	header = strings.TrimSpace(header)
	if decoded, err := base64.StdEncoding.DecodeString(header); err == nil {
		return strings.TrimSpace(string(decoded))
	}
	return header
}
