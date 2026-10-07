package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// reposPerPage is the maximum GitHub allows, chosen because enumerating a 20,000 repository
// organization costs one request per page and pages are the only lever available.
const reposPerPage = 100

// Repository is what the listing already tells us about a repository.
//
// DefaultBranch is empty when GitHub named none, which it does for a repository with no commits.
// Empty is "nobody said", never a stand-in for `main`: naming a branch on the repository's behalf
// would invent the fact the field exists to carry.
type Repository struct {
	FullName      string
	DefaultBranch string
	HasWorkflows  *bool
	// HasActionsDir and HasRootAction are `.github/actions` and a root `action.yml|yaml`, read
	// beside the workflow tree. Nil is a route that cannot look inside a tree, never "no".
	HasActionsDir *bool
	HasRootAction *bool
	// Fork is nil where the answer did not say; Visibility is empty there.
	Fork       *bool
	Visibility string
	// CreatedAt and PushedAt are what a sorted read stops below. Zero means GitHub named none,
	// which a repository with no commits genuinely has: it was created and never pushed, and
	// that is the case the creation-ordered walk exists for.
	CreatedAt time.Time
	PushedAt  time.Time
	// Properties is what the organization administers about this repository, one entry per value
	// so that a multi-select property is several rows rather than a string somebody has to parse.
	Properties []PropertyValue
	// PropertiesTruncated says the answer could not carry them all. Properties is then empty on
	// purpose: a partial dividing line puts a repository in the wrong unit, which is worse than
	// having no line at all.
	PropertiesTruncated bool
}

// ListRepositories returns the non-archived repositories an account holds.
//
// A declaration names accounts, and an account is an organization or a person. GitHub keeps those
// on different endpoints and answers 404 for the wrong one, so an organization is tried first and
// a user second — an organization asked through the user endpoint gets an answer, with the wrong
// list, which is worse than an error.
//
// Code scanning endpoints are per-repository, so this enumeration is the first of two
// repository-scoped passes: this one to discover repositories, then one lookup each to find
// which of them actually have a CodeQL database.
func (c *Client) ListRepositories(ctx context.Context, account string) ([]Repository, error) {
	if account == "" || strings.ContainsAny(account, "/?#") {
		return nil, fmt.Errorf("account %q is not a valid name", account)
	}

	repos, err := c.listRepositoriesFrom(ctx, fmt.Sprintf("%s/orgs/%s/repos?per_page=%d&type=all",
		c.baseURL, url.PathEscape(account), reposPerPage), account)
	if err == nil {
		return repos, nil
	}
	if !errors.Is(err, ErrNoDatabase) {
		return nil, err
	}
	// Not an organization. `type=all` is not a parameter the user endpoint takes.
	return c.listRepositoriesFrom(ctx, fmt.Sprintf("%s/users/%s/repos?per_page=%d",
		c.baseURL, url.PathEscape(account), reposPerPage), account)
}

func (c *Client) listRepositoriesFrom(
	ctx context.Context, next, account string,
) ([]Repository, error) {
	var repos []Repository
	for next != "" {
		resp, err := c.do(ctx, next, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}

		var page []struct {
			FullName string `json:"full_name"`
			Archived bool   `json:"archived"`
			// Already in this payload, so knowing it costs no additional request.
			DefaultBranch string `json:"default_branch"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding repository page for %s: %w", account, err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()

		for _, r := range page {
			// An archived repository cannot take a version bump, so counting it would
			// overstate every remediation estimate.
			if r.Archived || r.FullName == "" {
				continue
			}
			repos = append(repos, Repository{
				FullName: r.FullName, DefaultBranch: r.DefaultBranch})
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

func workflowFiles(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	found := false
	if string(raw) == "null" {
		return &found
	}
	var tree struct {
		Kind    string `json:"__typename"`
		Entries *[]struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil || tree.Kind == "" {
		return nil
	}
	if tree.Kind != "Tree" {
		return &found
	}
	if tree.Entries == nil {
		return nil
	}
	for _, entry := range *tree.Entries {
		name := strings.ToLower(entry.Name)
		if entry.Type == "blob" && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			found = true
			break
		}
	}
	return &found
}

// pathPresent reads one `object(expression:)` lookup: null is a path that is not there, and an
// absent field is a query that did not ask.
func pathPresent(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	found := false
	if string(raw) == "null" {
		return &found
	}
	var object struct {
		Kind string `json:"__typename"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Kind == "" {
		return nil
	}
	found = true
	return &found
}

// anyPresent is yes if either lookup found its path, and unknown only if neither was asked.
func anyPresent(a, b *bool) *bool {
	switch {
	case a != nil && *a:
		return a
	case b != nil && *b:
		return b
	case a != nil:
		return a
	}
	return b
}
