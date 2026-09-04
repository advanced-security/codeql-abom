package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// reposPerPage is the maximum GitHub allows, chosen because enumerating a 20,000 repository
// organisation costs one request per page and pages are the only lever available.
const reposPerPage = 100

// ListRepositories returns the non-archived repositories in an organisation.
//
// Code scanning endpoints are per-repository, so this enumeration is the first of two
// repository-scoped passes: this one to discover repositories, then one lookup each to find
// which of them actually have a CodeQL database.
func (c *Client) ListRepositories(ctx context.Context, org string) ([]string, error) {
	if org == "" || strings.ContainsAny(org, "/?#") {
		return nil, fmt.Errorf("organisation %q is not a valid name", org)
	}

	next := fmt.Sprintf("%s/orgs/%s/repos?per_page=%d&type=all",
		c.baseURL, url.PathEscape(org), reposPerPage)

	var repos []string
	for next != "" {
		resp, err := c.do(ctx, next, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}

		var page []struct {
			FullName string `json:"full_name"`
			Archived bool   `json:"archived"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding repository page for %s: %w", org, err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()

		for _, r := range page {
			// An archived repository cannot take a version bump, so counting it would
			// overstate every remediation estimate.
			if r.Archived || r.FullName == "" {
				continue
			}
			repos = append(repos, r.FullName)
		}
		next = nextLink(link)
	}
	return repos, nil
}

// nextLink extracts the rel="next" URL from a Link header, returning "" on the last page.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		target := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for _, attr := range segments[1:] {
			if strings.TrimSpace(attr) == `rel="next"` {
				return target[1 : len(target)-1]
			}
		}
	}
	return ""
}
