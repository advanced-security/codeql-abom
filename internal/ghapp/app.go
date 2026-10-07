package ghapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// DefaultAPIURL is GitHub.com's REST API.
const DefaultAPIURL = "https://api.github.com"

const (
	// refreshBefore replaces a token while a request made with it still has time to finish.
	refreshBefore = 5 * time.Minute
	// relistAfter bounds how often an owner nobody installed on can cost a listing.
	relistAfter = time.Minute
	apiVersion  = "2022-11-28"
	perPage     = 100
	maxAnswer   = 4 << 20
)

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// App acts as the GitHub App, and as each installation through a cached, hour-long token.
type App struct {
	Credentials Credentials
	HTTP        *http.Client
	Now         func() time.Time
	// PublicReads names the account whose installation reads for owners without one; empty takes the earliest.
	PublicReads string
	// API is the REST base requests go to, from the operator's configuration and never from a file
	// read back; empty is GitHub.com.
	API string

	mu       sync.Mutex
	byOwner  map[string]int64
	listed   []installation
	listedAt time.Time
	tokens   map[int64]minted
}

type minted struct {
	token   string
	expires time.Time
}

type installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	RepositorySelection string  `json:"repository_selection"`
	SuspendedAt         *string `json:"suspended_at"`
}

// statusError is GitHub refusing a request, with whatever it said about why.
type statusError struct {
	Status  int
	Message string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Message)
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) issuer() string {
	if a.Credentials.ClientID != "" {
		return a.Credentials.ClientID
	}
	return strconv.FormatInt(a.Credentials.AppID, 10)
}

// Token is owner's installation token or, lent when owner has none, the designated one's; it fails only with none to lend.
func (a *App) Token(ctx context.Context, owner string) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if owner != "" {
		id, err := a.installationFor(ctx, owner)
		if err == nil {
			token, err := a.tokenFor(ctx, id, owner)
			if !errors.Is(err, ErrNotInstalled) {
				return token, false, err
			}
		} else if !errors.Is(err, ErrNotInstalled) {
			return "", false, err
		}
	}
	id, account, err := a.designated(ctx)
	if err != nil {
		return "", false, err
	}
	token, err := a.tokenFor(ctx, id, account)
	return token, true, err
}

// designated is the named account's installation, or else the earliest one not suspended.
func (a *App) designated(ctx context.Context) (int64, string, error) {
	if a.listedAt.IsZero() {
		list, err := a.listInstallations(ctx)
		if err != nil {
			return 0, "", err
		}
		a.remember(list)
	}
	if a.PublicReads != "" {
		if id, ok := a.byOwner[strings.ToLower(a.PublicReads)]; ok {
			return id, a.PublicReads, nil
		}
		return 0, "", fmt.Errorf("%w: %s, the account named to read public repositories", ErrNotInstalled, a.PublicReads)
	}
	var chosen *installation
	for i, inst := range a.listed {
		if inst.SuspendedAt == nil && (chosen == nil || inst.ID < chosen.ID) {
			chosen = &a.listed[i]
		}
	}
	if chosen == nil {
		return 0, "", ErrNoInstallation
	}
	return chosen.ID, chosen.Account.Login, nil
}

// AccountToken returns owner's installation token, or ErrNotInstalled and no credential at all.
func (a *App) AccountToken(ctx context.Context, owner string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := a.installationFor(ctx, owner)
	if err != nil {
		return "", err
	}
	return a.tokenFor(ctx, id, owner)
}

func (a *App) installationFor(ctx context.Context, owner string) (int64, error) {
	key := strings.ToLower(owner)
	if id, ok := a.byOwner[key]; ok {
		return id, nil
	}
	if !a.now().Before(a.listedAt.Add(relistAfter)) {
		list, err := a.listInstallations(ctx)
		if err != nil {
			return 0, err
		}
		a.remember(list)
		if id, ok := a.byOwner[key]; ok {
			return id, nil
		}
	}
	return 0, fmt.Errorf("%w: %s", ErrNotInstalled, owner)
}

func (a *App) remember(list []installation) {
	a.listed = list
	a.byOwner = make(map[string]int64, len(list))
	listed := map[int64]bool{}
	for _, inst := range list {
		a.byOwner[strings.ToLower(inst.Account.Login)] = inst.ID
		listed[inst.ID] = true
	}
	for id := range a.tokens {
		if !listed[id] {
			delete(a.tokens, id)
		}
	}
	a.listedAt = a.now()
}

func (a *App) tokenFor(ctx context.Context, id int64, owner string) (string, error) {
	if t, ok := a.tokens[id]; ok && a.now().Before(t.expires.Add(-refreshBefore)) {
		return t.token, nil
	}
	jwt, err := signJWT(a.Credentials.Key, a.issuer(), a.now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = a.call(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", id), jwt, &out)
	if refused := new(statusError); errors.As(err, &refused) && refused.Status == http.StatusNotFound {
		delete(a.byOwner, strings.ToLower(owner))
		delete(a.tokens, id)
		// The listing still names it, so the next lookup reads a fresh one.
		a.listedAt = time.Time{}
		return "", fmt.Errorf("%w: %s (installation %d is gone)", ErrNotInstalled, owner, id)
	}
	if err != nil {
		return "", fmt.Errorf("minting a token for %s: %w", owner, err)
	}
	if out.Token == "" || out.ExpiresAt.IsZero() {
		return "", fmt.Errorf("minting a token for %s: GitHub answered without a token or its expiry", owner)
	}
	if a.tokens == nil {
		a.tokens = map[int64]minted{}
	}
	a.tokens[id] = minted{token: out.Token, expires: out.ExpiresAt}
	return out.Token, nil
}

func (a *App) listInstallations(ctx context.Context) ([]installation, error) {
	jwt, err := signJWT(a.Credentials.Key, a.issuer(), a.now())
	if err != nil {
		return nil, err
	}
	var all []installation
	for page := 1; ; page++ {
		var batch []installation
		path := fmt.Sprintf("/app/installations?per_page=%d&page=%d", perPage, page)
		if err := a.call(ctx, http.MethodGet, path, jwt, &batch); err != nil {
			return nil, fmt.Errorf("listing the App's installations: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			return all, nil
		}
	}
}

// Verify asks GitHub whether these credentials authenticate as this App.
func (a *App) Verify(ctx context.Context) error {
	jwt, err := signJWT(a.Credentials.Key, a.issuer(), a.now())
	if err != nil {
		return err
	}
	var got struct {
		ID int64 `json:"id"`
	}
	if err := a.call(ctx, http.MethodGet, "/app", jwt, &got); err != nil {
		return fmt.Errorf("asking GitHub which App this key signs for: %w", err)
	}
	if got.ID != a.Credentials.AppID {
		return fmt.Errorf("the key signs for App %d, not App %d", got.ID, a.Credentials.AppID)
	}
	return nil
}

// ReadInstallations lists the installations and their repositories; a failure is unread, never empty.
func (a *App) ReadInstallations(ctx context.Context) store.InstallationSync {
	a.mu.Lock()
	defer a.mu.Unlock()
	sync := store.InstallationSync{AppID: a.Credentials.AppID, At: a.now()}
	list, err := a.listInstallations(ctx)
	if err != nil {
		sync.Unread = err.Error()
		return sync
	}
	a.remember(list)
	for _, inst := range list {
		got := store.AppInstallation{
			Owner: inst.Account.Login, Kind: strings.ToLower(inst.Account.Type),
			InstallationID: inst.ID, RepositorySelection: inst.RepositorySelection,
			Suspended: inst.SuspendedAt != nil,
		}
		if got.RepositorySelection == "selected" && !got.Suspended {
			if names, err := a.installationRepositories(ctx, inst.ID, got.Owner); err != nil {
				got.RepositoriesUnread = err.Error()
			} else {
				got.Repositories = names
			}
		}
		sync.Installations = append(sync.Installations, got)
	}
	return sync
}

func (a *App) installationRepositories(ctx context.Context, id int64, owner string) ([]string, error) {
	token, err := a.tokenFor(ctx, id, owner)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for page := 1; ; page++ {
		var batch struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				Name string `json:"name"`
			} `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=%d&page=%d", perPage, page)
		if err := a.call(ctx, http.MethodGet, path, token, &batch); err != nil {
			return nil, fmt.Errorf("listing the repositories installed on %s: %w", owner, err)
		}
		for _, r := range batch.Repositories {
			names = append(names, r.Name)
		}
		if len(batch.Repositories) < perPage || len(names) >= batch.TotalCount {
			return names, nil
		}
	}
}

// call makes one request whose errors carry neither the bearer, anything in hide, nor the URL.
func (a *App) call(ctx context.Context, method, path, bearer string, into any, hide ...string) error {
	base := strings.TrimSuffix(a.API, "/")
	if base == "" {
		base = DefaultAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return fmt.Errorf("building a request to GitHub: %w", withoutURL(err))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := a.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		// Scrubbed before it is shortened, or a credential cut in half survives the scrub.
		return &statusError{Status: resp.StatusCode, Message: messageOf([]byte(scrub(string(body), append(hide, bearer)...)))}
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decoding GitHub's answer: %w", err)
	}
	return nil
}

// messageOf is GitHub's own explanation, bounded so an unexpected page cannot flood a log.
func messageOf(body []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		body = []byte(m.Message)
	}
	const limit = 300
	if len(body) > limit {
		body = body[:limit]
	}
	return strings.TrimSpace(string(body))
}

// scrub removes each secret from text. Secrets shorter than eight bytes are not credentials.
func scrub(text string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) >= 8 {
			text = strings.ReplaceAll(text, s, "[redacted]")
		}
	}
	return text
}

// withoutURL drops the URL net/http puts in a transport error: the manifest code travels in one.
func withoutURL(err error) error {
	if ue := new(url.Error); errors.As(err, &ue) {
		return fmt.Errorf("%s request to GitHub failed: %w", ue.Op, ue.Err)
	}
	return err
}
