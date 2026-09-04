// Package catalog fetches CodeQL databases from the GitHub code scanning API.
//
// The job queue and database catalog themselves live in mrva-system/mrvahepc; this package
// covers the gap they leave, which is the initial collection of databases for repositories
// where code scanning has produced one.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoDatabase reports that a repository has no CodeQL database for the requested language.
//
// In a large organization this is the common case rather than a failure: most repositories
// either have code scanning disabled or have never completed an analysis for the language.
var ErrNoDatabase = errors.New("no codeql database available")

// Database is the metadata GitHub reports for one stored CodeQL database.
type Database struct {
	Repository string
	Language   string
	// CommitOID identifies the source revision the database was built from. It is the value
	// the ingest ledger keys on to decide whether previously ingested results are still valid.
	CommitOID string
	Size      int64
	CreatedAt time.Time
	UpdatedAt time.Time
	URL       string
}

// Client talks to the GitHub code scanning CodeQL database endpoints.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	// MaxRetries bounds retries after rate limiting.
	MaxRetries int
	// MaxBackoff caps how long a single retry will wait, so a far-future reset time cannot
	// stall a worker indefinitely.
	MaxBackoff time.Duration

	sleep func(time.Duration)
}

// DefaultMaxRetries and DefaultMaxBackoff bound rate-limit handling.
const (
	DefaultMaxRetries = 5
	DefaultMaxBackoff = 2 * time.Minute
)

// New returns a client for the given API base URL, for example https://api.github.com.
func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      token,
		http:       hc,
		MaxRetries: DefaultMaxRetries,
		MaxBackoff: DefaultMaxBackoff,
		sleep:      time.Sleep,
	}
}

// ListDatabases returns every CodeQL database GitHub holds for the repository.
func (c *Client) ListDatabases(ctx context.Context, repository string) ([]Database, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/code-scanning/codeql/databases", c.baseURL, owner, name)
	resp, err := c.do(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload []struct {
		Language  string `json:"language"`
		CommitOID string `json:"commit_oid"`
		Size      int64  `json:"size"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		URL       string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding database list for %s: %w", repository, err)
	}

	dbs := make([]Database, 0, len(payload))
	for _, p := range payload {
		dbs = append(dbs, Database{
			Repository: repository,
			Language:   p.Language,
			CommitOID:  p.CommitOID,
			Size:       p.Size,
			CreatedAt:  parseTime(p.CreatedAt),
			UpdatedAt:  parseTime(p.UpdatedAt),
			URL:        p.URL,
		})
	}
	return dbs, nil
}

// FindDatabase returns the database for one language without downloading it.
//
// Callers are expected to consult the ingest ledger with the returned CommitOID before
// deciding to download, which is what keeps an unchanged organization cheap to re-scan.
func (c *Client) FindDatabase(ctx context.Context, repository, language string) (Database, error) {
	dbs, err := c.ListDatabases(ctx, repository)
	if err != nil {
		return Database{}, err
	}
	for _, db := range dbs {
		if strings.EqualFold(db.Language, language) {
			return db, nil
		}
	}
	return Database{}, fmt.Errorf("%s has no %s database: %w", repository, language, ErrNoDatabase)
}

// DownloadDatabase writes the database zip for one language to destPath.
//
// The download lands on a temporary file and is renamed only on success, so an interrupted
// run can never leave a truncated archive that a later run mistakes for a complete database.
func (c *Client) DownloadDatabase(ctx context.Context, repository, language, destPath string) error {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating download directory: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/code-scanning/codeql/databases/%s",
		c.baseURL, owner, name, language)
	resp, err := c.do(ctx, url, "application/zip")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".abom-download-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s %s database: %w", repository, language, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("finalizing download: %w", err)
	}
	return nil
}

// do issues a request, retrying while GitHub reports rate limiting.
func (c *Client) do(ctx context.Context, url, accept string) (*http.Response, error) {
	retries := c.MaxRetries
	if retries < 0 {
		retries = 0
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("requesting %s: %w", redact(url), err)
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil

		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %w", redact(url), ErrNoDatabase)

		case isRateLimited(resp):
			wait := c.backoff(resp)
			resp.Body.Close()
			if attempt >= retries {
				return nil, fmt.Errorf("rate limited by %s after %d retries", redact(url), retries)
			}
			c.sleep(wait)

		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("%s returned %d: %s",
				redact(url), resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

// isRateLimited distinguishes throttling from a genuine authorization failure: GitHub signals
// primary rate limits with 403 and an exhausted remaining count, and secondary limits with 429.
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.StatusCode != http.StatusForbidden {
		return false
	}
	return resp.Header.Get("x-ratelimit-remaining") == "0"
}

func (c *Client) backoff(resp *http.Response) time.Duration {
	max := c.MaxBackoff
	if max <= 0 {
		max = DefaultMaxBackoff
	}

	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return capDuration(time.Duration(secs)*time.Second, max)
		}
	}
	if v := resp.Header.Get("x-ratelimit-reset"); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Until(time.Unix(unix, 0)); d > 0 {
				return capDuration(d, max)
			}
		}
	}
	return time.Second
}

func capDuration(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}

func splitRepository(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("repository %q is not in owner/name form", repository)
	}
	return owner, name, nil
}

// redact strips any query string, which is where signed download URLs carry credentials.
func redact(url string) string {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		return url[:i] + "?<redacted>"
	}
	return url
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
