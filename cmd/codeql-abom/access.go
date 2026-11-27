package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/access"
	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/serve"
)

const (
	oidcSecretEnv   = "ABOM_OIDC_CLIENT_SECRET"
	githubSecretEnv = "ABOM_GITHUB_CLIENT_SECRET"
)

// accessFlags configure sign-in. The default is none: whoever is at this machine, behind the
// loopback boundary.
type accessFlags struct {
	mode, issuer, clientID, secretFile, redirect, rolesClaim, roleMap string

	githubRedirect, githubRoleMap, githubSecretFile string
	githubRefresh                                   time.Duration
}

func addAccessFlags(fs *flag.FlagSet) *accessFlags {
	c := &accessFlags{}
	fs.StringVar(&c.mode, "auth", "none", "who may use ABOM: none, only you at this machine; oidc, people signed in through an identity provider such as Entra ID; or github, people signed in with GitHub through the reading App")
	fs.StringVar(&c.githubRedirect, "github-redirect-url", "", "this deployment's /auth/callback, registered as a redirect URI of the reading App")
	fs.StringVar(&c.githubRoleMap, "github-role-map", "", "memberships as ABOM roles: org/team, org or org:owners, such as acme/abom-operators=operator,acme=viewer")
	fs.StringVar(&c.githubSecretFile, "github-client-secret-file", "", "file holding the reading App's client secret (default: $"+githubSecretEnv+")")
	fs.DurationVar(&c.githubRefresh, "github-role-refresh", 5*time.Minute, "how old a role read from GitHub may be before a change reads it again")
	fs.StringVar(&c.issuer, "oidc-issuer", "", "the provider's issuer, such as https://login.microsoftonline.com/<tenant>/v2.0")
	fs.StringVar(&c.clientID, "oidc-client-id", "", "this deployment's client ID with the provider")
	fs.StringVar(&c.secretFile, "oidc-client-secret-file", "", "file holding the client secret (default: $"+oidcSecretEnv+")")
	fs.StringVar(&c.redirect, "oidc-redirect-url", "", "this deployment's /auth/callback as registered with the provider")
	fs.StringVar(&c.rolesClaim, "oidc-roles-claim", "", "the ID token claim holding app roles (default: roles)")
	fs.StringVar(&c.roleMap, "oidc-role-map", "", "app role values as ABOM roles, such as Readers=viewer,Ops=operator (default: ABOM.Viewer, ABOM.Analyst, ABOM.Operator, ABOM.Admin)")
	return c
}

func (c accessFlags) config() (access.Config, error) {
	switch c.mode {
	case "", "none", "github":
		return access.Config{}, nil
	case "oidc":
	default:
		return access.Config{}, fmt.Errorf("--auth is none, oidc or github, not %q", c.mode)
	}
	if c.issuer == "" || c.clientID == "" || c.redirect == "" {
		return access.Config{}, errors.New("--auth=oidc needs --oidc-issuer, --oidc-client-id and --oidc-redirect-url")
	}
	cfg := access.Config{Issuer: c.issuer, ClientID: c.clientID, RedirectURL: c.redirect, RolesClaim: c.rolesClaim,
		ClientSecret: os.Getenv(oidcSecretEnv)}
	if c.secretFile != "" {
		body, err := os.ReadFile(c.secretFile)
		if err != nil {
			return access.Config{}, fmt.Errorf("--oidc-client-secret-file: %w", err)
		}
		cfg.ClientSecret = strings.TrimSpace(string(body))
	}
	if c.roleMap != "" {
		roles, err := roleMapOf("--oidc-role-map", c.roleMap)
		if err != nil {
			return access.Config{}, err
		}
		cfg.RoleMap = roles
	}
	return cfg, nil
}

// roleMapOf reads key=role pairs, refusing one that names no ABOM role.
func roleMapOf(flag, text string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(text, ",") {
		key, role, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || authz.Rank(role) == 0 {
			return nil, fmt.Errorf("%s: %q names no ABOM role; use viewer, analyst, operator or admin", flag, pair)
		}
		out[key] = role
	}
	return out, nil
}

// githubConfig signs people in through the reading App, which is given its client secret here.
func (c accessFlags) githubConfig(app *ghapp.App) (access.GitHubConfig, error) {
	if app == nil {
		return access.GitHubConfig{}, errors.New("--auth=github signs people in through the reading App; create it with codeql-abom app create")
	}
	if c.githubRedirect == "" || c.githubRoleMap == "" {
		return access.GitHubConfig{}, errors.New("--auth=github needs --github-redirect-url and --github-role-map")
	}
	secret := os.Getenv(githubSecretEnv)
	if c.githubSecretFile != "" {
		body, err := os.ReadFile(c.githubSecretFile)
		if err != nil {
			return access.GitHubConfig{}, fmt.Errorf("--github-client-secret-file: %w", err)
		}
		secret = strings.TrimSpace(string(body))
	}
	if secret == "" {
		return access.GitHubConfig{}, fmt.Errorf("--auth=github needs the reading App's client secret, generated in its "+
			"settings on GitHub, in --github-client-secret-file or $%s", githubSecretEnv)
	}
	roles, err := roleMapOf("--github-role-map", c.githubRoleMap)
	if err != nil {
		return access.GitHubConfig{}, err
	}
	app.Credentials.ClientSecret = secret
	return access.GitHubConfig{App: githubSignIn{app}, Web: app.Web(), RedirectURL: c.githubRedirect,
		RoleMap: roles, Refresh: c.githubRefresh}, nil
}

// githubSignIn is the reading App as sign-in asks for it.
type githubSignIn struct{ *ghapp.App }

func (g githubSignIn) SignIn(ctx context.Context, code, verifier, redirect string) (access.GitHubUser, error) {
	u, err := g.App.SignIn(ctx, code, verifier, redirect)
	return access.GitHubUser(u), err
}

// wireAccess turns sign-in on: every API request then needs a session, kept in the store so a restart
// signs nobody out, and the deployment answers on its registered host. It says who vouches for the
// people signing in.
func wireAccess(ctx context.Context, c accessFlags, app *ghapp.App, srv *serve.Server) (string, error) {
	var a *access.Authenticator
	var through string
	switch c.mode {
	case "github":
		cfg, err := c.githubConfig(app)
		if err != nil {
			return "", err
		}
		cfg.Log, cfg.Sessions = srv.Log, srv.Store
		if a, err = access.NewGitHub(cfg); err != nil {
			return "", err
		}
		through = "GitHub at " + cfg.Web
	default:
		cfg, err := c.config()
		if err != nil || cfg.Issuer == "" {
			return "", err
		}
		cfg.Log, cfg.Sessions = srv.Log, srv.Store
		if a, err = access.New(ctx, cfg); err != nil {
			return "", err
		}
		through = cfg.Issuer
	}
	srv.Access = a
	srv.AllowedHosts = append(srv.AllowedHosts, a.Host())
	return through, nil
}
