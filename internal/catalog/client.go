// Package catalog fetches CodeQL databases from the GitHub code scanning API.
//
// The job queue and database catalog themselves live in mrva-system/mrvahepc; this package
// covers the gap they leave, which is the initial collection of databases for repositories
// where code scanning has produced one.
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/work"
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
	// Cache lets an unchanged answer be recognized for a round trip and no primary quota.
	Cache ResponseCache
	// Pace spaces requests, and follows the budget GitHub reports rather than a number anybody
	// guessed. Nil asks as fast as the caller can.
	Pace *work.Limiter
	// REST and GraphQL are the pools this client's requests draw on, in the unit each charges.
	// Nil asks without limit and records nothing.
	REST    *work.TokenPool
	GraphQL *work.TokenPool
	// Tokens chooses each request's credential by the account it reads. Nil sends New's token, as before an App.
	Tokens ghapp.TokenSource
	// SourcePolicy applies only to remote source/result downloads, not metadata or eligibility probes.
	SourcePolicy func(context.Context, string) error
	// Route pins which API reads an account: RouteGraphQL, RouteREST, or empty for the default,
	// which prefers GraphQL and falls back to REST. Selection is the operator's and detection is
	// at most a default, so a pinned route is never second-guessed.
	Route string

	requests    atomic.Int64
	notModified atomic.Int64
	// points is what GraphQL charged, which is not a count of requests: one call may cost one
	// point or several, and only the answer knows which.
	points atomic.Int64

	budgetMu sync.Mutex
	budget   RateLimit
	// graphqlBudget is deliberately its own field. GraphQL and REST draw on separate allowances,
	// and letting one answer overwrite the other reports a crawl with points to spare as having
	// none, or the reverse.
	graphqlBudget RateLimit

	sleep func(time.Duration)
}

// RateLimit is what GitHub last said was left of the primary budget.
type RateLimit struct {
	// Known separates a budget nobody reported from a budget of nothing. Reporting "0 remaining"
	// for an account whose headers never arrived would stop a crawl entitled to run.
	Known     bool
	Remaining int
	Limit     int
	ResetAt   time.Time
}

// Stats is what a client has spent.
type Stats struct {
	// Requests counts answers received, including unchanged ones — a conditional request is still
	// a round trip, and reporting it as free would be a different lie from the one it fixes.
	Requests int
	// NotModified counts the answers GitHub said were unchanged.
	NotModified int
	// RateLimit is the REST budget the most recent answer reported.
	RateLimit RateLimit
	// GraphQL is the separate budget GraphQL draws on, reported apart from REST's because they
	// are separate allowances and one says nothing about the other.
	GraphQL RateLimit
	// Points is what GraphQL charged. Requests and points are different currencies, and adding
	// them would produce a number with no units.
	Points int
	// Waits counts requests the pace held back, and Waited is how long they were held for.
	// A crawl that waited is evidence the rate is wrong; one that did not is not.
	Waits  int
	Waited time.Duration
}

// Stats reports what this client has spent so far.
func (c *Client) Stats() Stats {
	c.budgetMu.Lock()
	budget := c.budget
	graphql := c.graphqlBudget
	c.budgetMu.Unlock()
	waits, waited := c.Pace.Waited()
	return Stats{
		Requests:    int(c.requests.Load()),
		NotModified: int(c.notModified.Load()),
		RateLimit:   budget,
		GraphQL:     graphql,
		Points:      int(c.points.Load()),
		Waits:       waits,
		Waited:      waited,
	}
}

// observeBudget records what an answer said was left, whatever its status code.
//
// Read here rather than only in `isRateLimited`, which runs after a 403: a crawl whose first news
// of its budget is that it has none cannot pace itself, only recover.
func (c *Client) observeBudget(resp *http.Response) {
	remaining, err := strconv.Atoi(resp.Header.Get("x-ratelimit-remaining"))
	if err != nil {
		return
	}
	seen := RateLimit{Known: true, Remaining: remaining}
	if v, err := strconv.Atoi(resp.Header.Get("x-ratelimit-limit")); err == nil {
		seen.Limit = v
	}
	if v, err := strconv.ParseInt(resp.Header.Get("x-ratelimit-reset"), 10, 64); err == nil {
		seen.ResetAt = time.Unix(v, 0)
	}
	// GitHub names which allowance the answer drew on. A points budget running low is no reason
	// to slow the per-repository probes, which spend requests from a different one, so the pace
	// follows REST and GraphQL's figure is recorded for the reader.
	resource := resp.Header.Get("x-ratelimit-resource")
	if strings.EqualFold(resource, work.ResourceGraphQL) {
		c.budgetMu.Lock()
		c.graphqlBudget = seen
		c.budgetMu.Unlock()
		c.GraphQL.Observe(seen.reading())
		return
	}
	c.budgetMu.Lock()
	c.budget = seen
	c.budgetMu.Unlock()
	// Search and the other REST allowances are separate budgets the pools do not spend.
	if resource == "" || strings.EqualFold(resource, work.ResourceCore) {
		c.REST.Observe(seen.reading())
	}
	c.followBudget(seen)
}

func (r RateLimit) reading() work.Reading {
	return work.Reading{Known: r.Known, Remaining: r.Remaining, Limit: r.Limit, ResetAt: r.ResetAt}
}

// restCharge is what GitHub charged one REST answer: nothing for one it answered unchanged, or for
// one it refused because nothing was left.
func restCharge(resp *http.Response) int {
	if resp.StatusCode == http.StatusNotModified || isRateLimited(resp) {
		return 0
	}
	return 1
}

// paceBelow is the share of the budget that must remain before requests are spaced out.
const paceBelow = 0.2

// followBudget spaces requests out once the budget is running low.
//
// Nothing is done for a budget that was never reported, because an unknown budget is not a budget
// of nothing and throttling on one would slow a crawl for a limit nobody stated. Nothing is done
// for an exhausted one either: zero requests an hour is how `SetPerHour` spells *unlimited*, so
// computing the rate there would make running out the fastest setting available. The 403 path
// already sleeps until the reset, and two mechanisms arguing over that is worse than one.
func (c *Client) followBudget(seen RateLimit) {
	if c.Pace == nil || !seen.Known || seen.Remaining <= 0 || seen.ResetAt.IsZero() {
		return
	}
	// Plenty left, so nothing to spread — and a budget that recovered has to lift its own throttle.
	if seen.Limit > 0 && float64(seen.Remaining) > float64(seen.Limit)*paceBelow {
		c.Pace.SetPerHour(0)
		return
	}
	until := time.Until(seen.ResetAt)
	if until <= 0 {
		return
	}
	c.Pace.SetPerHour(int(float64(seen.Remaining) * float64(time.Hour) / float64(until)))
}

// ResponseCache remembers what a URL last answered, so GitHub can be asked whether that answer
// still stands rather than asked for it again.
//
// A miss and a failure are the same thing to a caller — both mean "ask in full" — which is why
// nothing here returns an error: a cache that cannot answer must never be able to fail a read.
type ResponseCache interface {
	Lookup(url string) (etag string, body []byte, ok bool)
	Save(url, etag string, body []byte)
}

// jsonAccept is the only media type this client will remember. An allowlist rather than an
// exclusion of `application/zip`, so a binary media type added later is not cached by omission.
const jsonAccept = "application/vnd.github+json"

// maxCachedBody bounds what one entry may cost. A page of repositories is tens of kilobytes.
const maxCachedBody = 2 << 20

// readCloser reattaches a closer to a reader that has been wrapped.
type readCloser struct {
	io.Reader
	io.Closer
}

type answerState struct {
	url       string
	resp      *http.Response
	held      []byte
	cacheable bool
	attempt   int
	retries   int
	// lent names the owner a lent token read for, so a refusal can say its installation is missing.
	lent string
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
	if err := c.allowSource(ctx, repository); err != nil {
		return err
	}
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
	return save(resp.Body, destPath, repository+" "+language+" database")
}

// save writes a download to destPath through a temporary file renamed only once it is complete.
func save(body io.Reader, destPath, what string) error {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".abom-download-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", what, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("finalizing download: %w", err)
	}
	return nil
}

// do issues a request, retrying while GitHub reports rate limiting. A JSON read is conditional on
// what the cache holds for its URL.
func (c *Client) do(ctx context.Context, url, accept string) (*http.Response, error) {
	return c.get(ctx, url, accept, c.Cache != nil && accept == jsonAccept)
}

// doUncached is do for an answer asked for once, which a cache would keep forever and never read.
func (c *Client) doUncached(ctx context.Context, url, accept string) (*http.Response, error) {
	return c.get(ctx, url, accept, false)
}

func (c *Client) get(ctx context.Context, url, accept string, cacheable bool) (*http.Response, error) {
	retries := c.retries()

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, held, lent, err := c.request(ctx, url, accept, cacheable)
		if err != nil {
			return nil, err
		}

		// A client that never retries has a cheaper fallback than waiting for the reset.
		if retries == 0 {
			if reset, spent := c.REST.Exhausted(); spent {
				return nil, fmt.Errorf("rate limited by %s until %s", redact(url), reset.UTC().Format(time.RFC3339))
			}
		}
		if err := c.REST.Wait(ctx); err != nil {
			return nil, err
		}
		if err := c.Pace.Wait(ctx); err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("requesting %s: %w", redact(url), err)
		}
		c.requests.Add(1)
		c.observeBudget(resp)
		c.REST.Spend(1, restCharge(resp))
		answer, retry, err := c.answer(answerState{
			url: url, resp: resp, held: held, cacheable: cacheable, attempt: attempt, retries: retries, lent: lent,
		})
		if !retry {
			return answer, err
		}
	}
}

func (c *Client) retries() int {
	if c.MaxRetries < 0 {
		return 0
	}
	return c.MaxRetries
}

func (c *Client) request(
	ctx context.Context, url, accept string, cacheable bool,
) (*http.Request, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	owner, account := ghapp.Subject(url)
	lent, err := c.authorize(ctx, req, owner, account)
	if err != nil {
		return nil, nil, "", err
	}

	var held []byte
	if cacheable {
		if etag, body, ok := c.Cache.Lookup(url); ok && etag != "" {
			req.Header.Set("If-None-Match", etag)
			held = body
		}
	}
	return req, held, lent, nil
}

// authorize sets a request's credential, and names the owner a lent token stood in for.
func (c *Client) authorize(ctx context.Context, req *http.Request, owner string, account bool) (string, error) {
	if c.Tokens == nil {
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		return "", nil
	}
	var token string
	var lent bool
	var err error
	if account {
		token, err = c.Tokens.AccountToken(ctx, owner)
	} else {
		token, lent, err = c.Tokens.Token(ctx, owner)
	}
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if lent {
		return owner, nil
	}
	return "", nil
}

func (c *Client) answer(state answerState) (*http.Response, bool, error) {
	switch {
	case state.resp.StatusCode == http.StatusOK:
		if !state.cacheable {
			return state.resp, false, nil
		}
		remembered, err := c.remember(state.resp, state.url)
		return remembered, false, err

	case state.resp.StatusCode == http.StatusNotModified:
		c.notModified.Add(1)
		return cachedResponse(state.url, state.resp, state.held)

	case isRateLimited(state.resp):
		wait := c.backoff(state.resp)
		state.resp.Body.Close()
		if state.attempt >= state.retries {
			return nil, false, fmt.Errorf("rate limited by %s after %d retries", redact(state.url), state.retries)
		}
		c.sleep(wait)
		return nil, true, nil

	default:
		body, _ := io.ReadAll(io.LimitReader(state.resp.Body, 512))
		state.resp.Body.Close()
		status := &StatusError{
			URL: redact(state.url), Code: state.resp.StatusCode,
			Body: strings.TrimSpace(string(body)), Message: githubMessage(body),
		}
		if code := state.resp.StatusCode; code == http.StatusNotFound || code == http.StatusForbidden {
			status.Uninstalled = state.lent
		}
		return nil, false, status
	}
}

func cachedResponse(url string, resp *http.Response, held []byte) (*http.Response, bool, error) {
	resp.Body.Close()
	if held == nil {
		// Answering empty here would report an account as holding nothing.
		return nil, false, fmt.Errorf("%s answered 304 and nothing was held to answer with",
			redact(url))
	}
	// Presented as 200 because it is the same answer, established rather than assumed.
	resp.StatusCode = http.StatusOK
	resp.Body = io.NopCloser(bytes.NewReader(held))
	return resp, false, nil
}

// StatusError is an answer GitHub gave that was not the one asked for, with its own reason.
//
// A 404 still unwraps to ErrNoDatabase.
type StatusError struct {
	URL  string
	Code int
	Body string
	// Message is GitHub's `message` field, empty where the body carried none.
	Message string
	// Uninstalled names an owner read with a lent token, which cannot see its private repositories.
	Uninstalled string
}

func (e *StatusError) Error() string {
	text := fmt.Sprintf("%s returned %d: %s", e.URL, e.Code, e.Body)
	if e.Code == http.StatusNotFound {
		text = fmt.Sprintf("%s: %s", e.URL, ErrNoDatabase)
	}
	if e.Uninstalled != "" {
		text += fmt.Sprintf("; %s: %s, so a private repository there cannot be read", ghapp.ErrNotInstalled, e.Uninstalled)
	}
	return text
}

func (e *StatusError) Unwrap() []error {
	var causes []error
	if e.Code == http.StatusNotFound {
		causes = append(causes, ErrNoDatabase)
	}
	if e.Uninstalled != "" {
		causes = append(causes, ghapp.ErrNotInstalled)
	}
	return causes
}

// githubMessage reads the `message` GitHub puts in an error body, and nothing when there is none.
func githubMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Message)
}

// remember buffers an answer so a later 304 has something to replay.
//
// The body is read here rather than by the caller, so an answer too large to hold still has to
// reach the caller whole — truncating it to fit a cache would corrupt the read it was meant to
// make cheaper.
func (c *Client) remember(resp *http.Response, url string) (*http.Response, error) {
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return resp, nil
	}

	original := resp.Body
	head, err := io.ReadAll(io.LimitReader(original, maxCachedBody+1))
	if err != nil {
		original.Close()
		return nil, fmt.Errorf("reading %s: %w", redact(url), err)
	}
	if len(head) > maxCachedBody {
		resp.Body = readCloser{io.MultiReader(bytes.NewReader(head), original), original}
		return resp, nil
	}

	original.Close()
	c.Cache.Save(url, etag, head)
	resp.Body = io.NopCloser(bytes.NewReader(head))
	return resp, nil
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

var (
	ownerName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)
	repositoryName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// splitRepository reads owner/name, refusing anything GitHub could not have named: every caller
// puts it in a URL, and some read it from a webhook payload.
func splitRepository(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || !ownerName.MatchString(owner) || !repositoryName.MatchString(name) || name == "." || name == ".." {
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
