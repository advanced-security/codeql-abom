package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client writes snapshots to GitHub and reads back what GitHub made of them.
//
// A submission cannot be withdrawn and there is no delete endpoint, so the allow-list is a code
// guard rather than a convention.
type Client struct {
	Token   string
	Allowed map[string]bool
	BaseURL string
	HTTP    *http.Client
}

func NewClient(token string, allowed []string) *Client {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[strings.ToLower(strings.TrimSpace(a))] = true
	}
	return &Client{
		Token:   token,
		Allowed: set,
		BaseURL: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type Result struct {
	ID      int64  `json:"id"`
	Result  string `json:"result"`
	Message string `json:"message"`
}

// ErrNotApproved reports a target outside the configured allow-list.
type ErrNotApproved struct{ Repository string }

func (e *ErrNotApproved) Error() string {
	return fmt.Sprintf("%s is not an approved submission target", e.Repository)
}

// NotApproved marks this as a refusal rather than a failure, so the transport can answer 403
// without importing this package.
func (e *ErrNotApproved) NotApproved() {}

// Submit writes a snapshot, refusing anything that is not an approved target or that would claim
// a path GitHub analyzes for itself.
func (c *Client) Submit(ctx context.Context, m Metadata, snap Snapshot) (Result, error) {
	if !c.Allowed[strings.ToLower(m.Repository)] {
		return Result{}, &ErrNotApproved{Repository: m.Repository}
	}
	for name, mf := range snap.Manifests {
		if strings.HasPrefix(mf.File.SourceLocation, ".github/workflows/") {
			return Result{}, fmt.Errorf(
				"manifest %q claims %s, which GitHub analyzes: submitting it would replace "+
					"GitHub's own findings for that file", name, mf.File.SourceLocation)
		}
	}

	body, err := json.Marshal(snap)
	if err != nil {
		return Result{}, err
	}
	url := fmt.Sprintf("%s/repos/%s/dependency-graph/snapshots", c.BaseURL, m.Repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	c.headers(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, fmt.Errorf("snapshot rejected: %s: %s", resp.Status, b)
	}
	var out Result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{}, err
	}
	return out, nil
}

func (c *Client) headers(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}
