package access

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// GitHubUser is a person GitHub vouched for: their account ID, which a renamed login keeps, the
// login and their display name.
type GitHubUser struct {
	ID    int64
	Login string
	Name  string
}

// GitHub is the App people sign in through and whose installations read their memberships.
type GitHub interface {
	AuthorizeURL(state, challenge, redirect string) string
	SignIn(ctx context.Context, code, verifier, redirect string) (GitHubUser, error)
	// Role is login's role in org, "admin" for an owner, or in org's team when team is named; empty
	// when they are not an active member.
	Role(ctx context.Context, org, team, login string) (string, error)
}

// GitHubConfig signs people in through ABOM's own App.
type GitHubConfig struct {
	App GitHub
	// Web is where people sign in, such as https://github.com; it qualifies every account ID, as an
	// issuer does a subject.
	Web         string
	RedirectURL string
	// RoleMap grants ABOM roles by membership: "org/team" for a team's members, "org" for every
	// member of the organization, and "org:owners" for its owners alone.
	RoleMap map[string]string
	// Refresh is how old a role may be before a request that changes something reads it again.
	Refresh    time.Duration
	SessionTTL time.Duration
	Log        io.Writer
}

// membershipGrant is one role map entry: whose membership grants role.
type membershipGrant struct {
	org, team string
	owners    bool
	key, role string
}

type gitHubProvider struct {
	app      GitHub
	web      string
	redirect string
	grants   []membershipGrant
}

// NewGitHub readies sign-in through GitHub. A role map entry naming no organization, or no ABOM
// role, is refused here rather than read as granting nothing.
func NewGitHub(cfg GitHubConfig) (*Authenticator, error) {
	if cfg.App == nil || cfg.Web == "" || cfg.RedirectURL == "" {
		return nil, errors.New("access: an App, its web address and a redirect URL are required")
	}
	if len(cfg.RoleMap) == 0 {
		return nil, errors.New("access: a GitHub role map is required, or nobody who signs in holds a role")
	}
	p := &gitHubProvider{app: cfg.App, web: strings.TrimSuffix(cfg.Web, "/"), redirect: cfg.RedirectURL}
	for key, role := range cfg.RoleMap {
		if authz.Rank(role) == 0 {
			return nil, fmt.Errorf("access: %s=%s names no ABOM role; use viewer, analyst, operator or admin", key, role)
		}
		g := membershipGrant{key: key, role: role}
		group, suffix, owners := strings.Cut(key, ":")
		g.org, g.team, _ = strings.Cut(group, "/")
		if g.org == "" || (owners && (suffix != "owners" || g.team != "")) || strings.Contains(g.team, "/") {
			return nil, fmt.Errorf("access: %q is not org, org/team or org:owners", key)
		}
		g.owners = owners
		p.grants = append(p.grants, g)
	}
	return authenticate(p, cfg.RedirectURL, cfg.SessionTTL, cfg.Refresh, cfg.Log)
}

func (g *gitHubProvider) authorizeURL(p pending) string {
	sum := sha256.Sum256([]byte(p.verifier))
	return g.app.AuthorizeURL(p.state, base64.RawURLEncoding.EncodeToString(sum[:]), g.redirect)
}

func (g *gitHubProvider) identify(ctx context.Context, code string, p pending) (User, error) {
	who, err := g.app.SignIn(ctx, code, p.verifier, g.redirect)
	if err != nil {
		return User{}, fmt.Errorf("GitHub did not sign them in: %w", err)
	}
	u := User{ID: g.web + "#" + strconv.FormatInt(who.ID, 10), Name: who.Name, Username: who.Login, Claimed: []string{}}
	if u.Role, u.Claimed, err = g.roleOf(ctx, who.Login); err != nil {
		return User{}, fmt.Errorf("%s's memberships could not be read: %w", who.Login, err)
	}
	return u, nil
}

func (g *gitHubProvider) reread(ctx context.Context, u User) (string, bool, error) {
	role, _, err := g.roleOf(ctx, u.Username)
	return role, true, err
}

// roleOf is the highest role any membership grants, and the entries that granted one.
func (g *gitHubProvider) roleOf(ctx context.Context, login string) (string, []string, error) {
	read := map[string]string{}
	membership := func(org, team string) (string, error) {
		key := org + "/" + team
		if role, ok := read[key]; ok {
			return role, nil
		}
		role, err := g.app.Role(ctx, org, team, login)
		read[key] = role
		return role, err
	}
	best, claimed := "", []string{}
	for _, gr := range g.grants {
		role, err := membership(gr.org, gr.team)
		if err != nil {
			return "", nil, err
		}
		if role == "" || (gr.owners && role != "admin") {
			continue
		}
		claimed = append(claimed, gr.key)
		if authz.Rank(gr.role) > authz.Rank(best) {
			best = gr.role
		}
	}
	return best, claimed, nil
}
