package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ReadRepository reads one repository as a listing that names it alone: never full, so it records
// what it sees and nothing as missing. No listing names an archived repository, so this does not.
func (c *Client) ReadRepository(ctx context.Context, fullName string) (Listing, error) {
	owner, repo, err := splitRepository(fullName)
	if err != nil {
		return Listing{}, err
	}
	owner, repo = url.PathEscape(owner), url.PathEscape(repo)
	out := Listing{Route: RouteREST, Repositories: []Repository{}}
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s", c.baseURL, owner, repo), jsonAccept)
	if err != nil {
		return out, err
	}
	var r struct {
		FullName      string `json:"full_name"`
		Archived      bool   `json:"archived"`
		DefaultBranch string `json:"default_branch"`
		CreatedAt     string `json:"created_at"`
		PushedAt      string `json:"pushed_at"`
		Fork          *bool  `json:"fork"`
		Visibility    string `json:"visibility"`
	}
	err = json.NewDecoder(resp.Body).Decode(&r)
	resp.Body.Close()
	if err != nil {
		return out, fmt.Errorf("decoding %s: %w", fullName, err)
	}
	out.Pages = 1
	if r.Archived || r.FullName == "" {
		return out, nil
	}
	read := Repository{
		FullName:      r.FullName,
		DefaultBranch: r.DefaultBranch,
		CreatedAt:     parseTimestamp(r.CreatedAt),
		PushedAt:      parseTimestamp(r.PushedAt),
		Fork:          r.Fork,
		Visibility:    r.Visibility,
	}
	read.Properties, out.PropertiesRead, out.PropertiesUnread = c.repositoryProperties(ctx, owner, repo)
	out.Repositories = append(out.Repositories, read)
	return out, nil
}

// repositoryProperties reads one repository's custom property values, which Metadata covers.
func (c *Client) repositoryProperties(ctx context.Context, owner, repo string) ([]PropertyValue, bool, string) {
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/properties/values", c.baseURL, owner, repo), jsonAccept)
	if err != nil {
		return nil, false, err.Error()
	}
	defer resp.Body.Close()
	var entries []struct {
		PropertyName string          `json:"property_name"`
		Value        json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, false, fmt.Sprintf("decoding property values for %s/%s: %v", owner, repo, err)
	}
	var out []PropertyValue
	for _, e := range entries {
		out = append(out, propertyNode{PropertyName: e.PropertyName, Value: e.Value}.values()...)
	}
	return out, true, ""
}
