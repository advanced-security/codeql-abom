package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// HeadSHA returns the commit a repository's default branch currently points at.
//
// A dependency snapshot carries the commit it describes. GitHub does not gate the submission on
// it — measured 2026-09-08, a snapshot naming a non-HEAD commit was applied anyway — so this is
// provenance rather than a validity check, and a wrong value produces a submission that is live
// and mislabeled rather than one that is rejected.
func (c *Client) HeadSHA(ctx context.Context, repository string) (string, error) {
	if repository == "" || strings.Count(repository, "/") != 1 {
		return "", fmt.Errorf("repository %q is not owner/name", repository)
	}

	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/commits/HEAD", c.baseURL, repository),
		"application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.SHA == "" {
		return "", fmt.Errorf("%s returned no commit for HEAD", repository)
	}
	return out.SHA, nil
}
