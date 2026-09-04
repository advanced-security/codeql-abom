package advisory

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// DefaultBatchSize is how many packages one query names.
//
// The API accepts far more, but the whole batch travels in the query string, so batches are
// kept small enough that a long list of owner/repo names cannot overrun a URL length limit.
const DefaultBatchSize = 50

// DefaultBaseURL is the public API.
const DefaultBaseURL = "https://api.github.com"

// Client queries advisories for a set of packages.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	// Token authenticates requests. Without one the API allows 60 requests an hour, which is
	// too few for an organization-sized package list.
	Token     string
	BatchSize int
	Pace      *work.Limiter
	// OnBatch reports progress; batches is the total, done the number completed.
	OnBatch func(done, batches, advisories int)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimSuffix(c.BaseURL, "/")
}

func (c *Client) batchSize() int {
	if c.BatchSize <= 0 {
		return DefaultBatchSize
	}
	return c.BatchSize
}

// Fetch returns every advisory affecting the given packages.
//
// Packages must be repository-scoped names such as "actions/checkout"; the API does not know
// about subpath actions.
func (c *Client) Fetch(ctx context.Context, packages []string) ([]Advisory, error) {
	batches := Batch(packages, c.batchSize())

	seen := map[string]bool{}
	var out []Advisory
	for i, batch := range batches {
		next := c.baseURL() + "/advisories?" + url.Values{
			"ecosystem": {"actions"},
			"per_page":  {"100"},
			"affects":   {strings.Join(batch, ",")},
		}.Encode()

		// A batch can span pages, and stopping at the first would silently under-report.
		for next != "" {
			if err := c.Pace.Wait(ctx); err != nil {
				return out, err
			}
			page, following, err := c.get(ctx, next)
			if err != nil {
				return out, err
			}
			for _, a := range page {
				if seen[a.GHSAID] {
					continue
				}
				seen[a.GHSAID] = true
				out = append(out, a)
			}
			next = following
		}

		if c.OnBatch != nil {
			c.OnBatch(i+1, len(batches), len(out))
		}
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, endpoint string) ([]Advisory, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("querying advisories: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining == "0" {
			return nil, "", fmt.Errorf(
				"advisory rate limit exhausted (resets at %s); set GITHUB_TOKEN to raise it",
				resp.Header.Get("X-RateLimit-Reset"))
		}
		return nil, "", fmt.Errorf("advisories returned %s: %s",
			resp.Status, strings.TrimSpace(string(body)))
	}

	page, err := Parse(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return page, nextLink(resp.Header.Get("Link")), nil
}

// nextRel matches the cursor-paginated next link; the API paginates by cursor, so the URL
// cannot be reconstructed from a page number.
var nextRel = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func nextLink(header string) string {
	if m := nextRel.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

// Batch splits packages into query-sized groups.
func Batch(packages []string, size int) [][]string {
	if size <= 0 {
		size = DefaultBatchSize
	}
	var out [][]string
	for start := 0; start < len(packages); start += size {
		end := start + size
		if end > len(packages) {
			end = len(packages)
		}
		out = append(out, packages[start:end])
	}
	return out
}
