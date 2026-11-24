package ghapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
)

// Bot is the login GitHub records for what the App does.
func (a *App) Bot() string { return a.Credentials.Slug + "[bot]" }

// Host is the GitHub host the App's API serves, which every token it mints is for.
func (a *App) Host() string {
	u, err := url.Parse(strings.TrimSuffix(a.API, "/"))
	if err != nil || u.Host == "" || a.API == "" {
		return "github.com"
	}
	if host, ok := strings.CutPrefix(u.Host, "api."); ok {
		return host
	}
	return u.Host
}

// Identity is the App and the accounts it is installed on, not suspended: with an App, being
// installed on an account is what makes it eligible to be declared, and nobody's gh session is read.
func (a *App) Identity(ctx context.Context) (ghauth.Identity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	list, err := a.listInstallations(ctx)
	if err != nil {
		return ghauth.Identity{}, &ghauth.Problem{
			Reason: "The App's installations could not be read, so no account can be confirmed: " + err.Error(),
			Remedy: "Check the App with: codeql-abom app status",
		}
	}
	a.remember(list)
	id := ghauth.Identity{Login: a.Bot(), Host: a.Host(), VerifiedAt: a.now().UTC(), Accounts: []ghauth.Account{}}
	for _, inst := range list {
		if inst.SuspendedAt != nil || inst.Account.Login == "" {
			continue
		}
		kind := ghauth.KindOrganization
		if strings.EqualFold(inst.Account.Type, "user") {
			kind = ghauth.KindUser
		}
		id.Accounts = append(id.Accounts, ghauth.Account{Owner: inst.Account.Login, Kind: kind, Role: ghauth.RoleInstallation})
	}
	sort.Slice(id.Accounts, func(i, j int) bool {
		return strings.ToLower(id.Accounts[i].Owner) < strings.ToLower(id.Accounts[j].Owner)
	})
	return id, nil
}

// Repositories lists what owner's own installation includes, which is what the App can read there.
func (a *App) Repositories(ctx context.Context, owner string) ([]ghauth.Repository, error) {
	if !ghauth.IsAccountName(owner) {
		return nil, fmt.Errorf("%q is not a GitHub account name", owner)
	}
	token, err := a.AccountToken(ctx, owner)
	if err != nil {
		return nil, a.problem(owner, "its repositories", err)
	}
	out := []ghauth.Repository{}
	for path := fmt.Sprintf("/installation/repositories?per_page=%d", perPage); path != ""; {
		var page struct {
			Repositories []struct {
				ghauth.Repository
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		answer, header, err := a.exchange(ctx, http.MethodGet, path, token, nil)
		if err == nil {
			err = json.Unmarshal(answer, &page)
		}
		if err != nil {
			return nil, a.problem(owner, "its repositories", err)
		}
		for _, r := range page.Repositories {
			if r.Name != "" && strings.EqualFold(r.Owner.Login, owner) && !ghauth.IsAdvisoryFork(r.Name) {
				out = append(out, ghauth.Repository{Name: r.Name, Private: r.Private, Archived: r.Archived, Fork: r.Fork})
			}
		}
		if path, err = a.next(header); err != nil {
			return nil, a.problem(owner, "its repositories", err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// pickerPath is the one shape of read the Runner serves: an organization's custom properties.
var pickerPath = regexp.MustCompile(`^orgs/([A-Za-z0-9][A-Za-z0-9-]{0,38})/(properties/(schema|values)|repos)([?].*)?$`)

// Runner answers the reads ghauth makes for the property picker, `gh api <path> [--paginate]`, through
// the named organization's own installation. Anything else is refused: the App has no person to ask about.
func (a *App) Runner() func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		var path string
		paginate := false
		for _, arg := range args {
			switch {
			case arg == "--paginate":
				paginate = true
			case arg == "api" && path == "", strings.HasPrefix(arg, "-"):
			default:
				path = arg
			}
		}
		m := pickerPath.FindStringSubmatch(path)
		if name != "gh" || m == nil {
			return nil, fmt.Errorf("the App reads an organization's properties and repositories for the picker, not %q", path)
		}
		owner := m[1]
		token, err := a.AccountToken(ctx, owner)
		if err != nil {
			return nil, a.problem(owner, "its custom properties", err)
		}
		var out bytes.Buffer
		for next := "/" + path; next != ""; {
			answer, header, err := a.exchange(ctx, http.MethodGet, next, token, nil)
			if err != nil {
				return nil, a.problem(owner, "its custom properties", err)
			}
			out.Write(answer)
			if next = ""; paginate {
				if next, err = a.next(header); err != nil {
					return nil, a.problem(owner, "its custom properties", err)
				}
			}
		}
		return out.Bytes(), nil
	}
}

// problem says why the App could not read what about owner, without sending anyone to gh.
func (a *App) problem(owner, what string, err error) *ghauth.Problem {
	switch refused := new(statusError); {
	case errors.Is(err, ErrNotInstalled):
		return &ghauth.Problem{
			Reason: "The App is not installed on " + owner + ", so " + what + " cannot be read.",
			Remedy: "An owner of " + owner + " installs the App, then try again.",
		}
	case errors.Is(err, ErrNotGranted):
		return &ghauth.Problem{Reason: err.Error(), Remedy: "An owner of " + owner + " approves the permission, then try again."}
	case errors.As(err, &refused) && refused.Status == http.StatusNotFound:
		return &ghauth.Problem{
			Reason: "The App cannot see " + what + " on " + owner + ".",
			Remedy: "Check the name; only an organization has custom properties.",
		}
	default:
		return &ghauth.Problem{
			Reason: "The App could not read " + owner + "'s " + strings.TrimPrefix(what, "its ") + ": " + err.Error(),
			Remedy: "Check the App with: codeql-abom app status",
		}
	}
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// next is the path of the page after this one, refusing a link to anywhere but the configured API:
// the token would follow it.
func (a *App) next(header http.Header) (string, error) {
	m := nextLink.FindStringSubmatch(header.Get("Link"))
	if m == nil {
		return "", nil
	}
	path, ok := strings.CutPrefix(m[1], a.base())
	if !ok || !strings.HasPrefix(path, "/") {
		return "", errors.New("GitHub's next page is not on the configured API")
	}
	return path, nil
}
