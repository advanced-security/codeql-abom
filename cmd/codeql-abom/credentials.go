package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/submit"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

// installationSyncEvery is how often serve rereads the App's installations into the store.
const installationSyncEvery = time.Hour

// readerApp is the App configured in dir, or nil with none, which leaves every reader exactly as before.
func readerApp(dir, publicReads, apiURL string) (*ghapp.App, error) {
	if !ghapp.Configured(dir) {
		if publicReads != "" {
			return nil, fmt.Errorf("an account is named for public reads, but no GitHub App is configured in %s", dir)
		}
		return nil, nil
	}
	creds, err := ghapp.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("loading the GitHub App in %s: %w", dir, err)
	}
	return &ghapp.App{Credentials: creds, PublicReads: publicReads, API: apiURL}, nil
}

// readerTokens is app as a token source; without one it is a nil interface, never a nil App inside one.
func readerTokens(app *ghapp.App) ghapp.TokenSource {
	if app == nil {
		return nil
	}
	return app
}

// newReader is a catalog client reading through tokens, or with the operator's token when there are none.
func newReader(ctx context.Context, apiURL string, tokens ghapp.TokenSource) *catalog.Client {
	if tokens == nil {
		return catalog.New(apiURL, githubToken(ctx), nil)
	}
	c := catalog.New(apiURL, "", nil)
	c.Tokens = tokens
	return c
}

// eventSecret is the App's webhook secret when one is configured, refusing a different one given beside it.
func eventSecret(app *ghapp.App, dir string, given []byte) ([]byte, error) {
	if app == nil {
		return given, nil
	}
	secret, err := ghapp.WebhookSecret(dir)
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	if len(given) > 0 && !bytes.Equal(given, secret) {
		return nil, errors.New("serve: --webhook-secret-file or $" + webhookSecretEnv +
			" is not the App's webhook secret, and every delivery would be refused; drop it to use the App's")
	}
	return secret, nil
}

// syncInstallations records the App's installations now, on every tick, and whenever an installation event wakes it.
func syncInstallations(
	ctx context.Context, app *ghapp.App, s store.Backend, every time.Duration, wake <-chan struct{}, log io.Writer,
) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		sync := app.ReadInstallations(ctx)
		if err := s.RecordInstallationSync(sync); err != nil {
			fmt.Fprintf(log, "%s serve: recording the App's installations: %v\n", time.Now().UTC().Format(time.RFC3339), err)
		} else if sync.Unread != "" {
			fmt.Fprintf(log, "%s serve: the App's installations could not be read, so each stays unknown: %s\n",
				time.Now().UTC().Format(time.RFC3339), sync.Unread)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

// appDirEnv names the reading App's directory to every command, as ABOM_DB names the database: in
// a pod, a command run beside the server reads as the same App.
const appDirEnv = "ABOM_APP_DIR"

// readerAppDir is where a command naming dbPath finds the reading App: the directory the
// environment names, else the one beside a SQLite database. A PostgreSQL one has nothing beside it.
func readerAppDir(dbPath string) string {
	return cmp.Or(os.Getenv(appDirEnv), ghapp.DefaultDir(dbPath))
}

// dbReaderTokens is the token source for a command naming a database: its reading App, or nil for your own token.
func dbReaderTokens(dbPath string) (ghapp.TokenSource, error) {
	app, err := readerApp(readerAppDir(dbPath), "", defaultAPIURL())
	return readerTokens(app), err
}

// githubCredentials chooses what each kind of call carries. With an App, every token is minted by it
// for that call and the person's is never read, because a production pod has no gh session;
// without one, the person's token is used throughout, which is the local and development path.
type githubCredentials struct {
	app *ghapp.App
	// operator reads the person's token; only the path without an App calls it.
	operator func(context.Context) string
	// copilotOrg is the organization an App's Copilot requests are billed to.
	copilotOrg string
}

// newCredentials bills Copilot to the App's own organization unless another is named.
func newCredentials(app *ghapp.App, copilotOrg string) githubCredentials {
	if app != nil && copilotOrg == "" && app.Credentials.OwnerKind != ghauth.KindUser {
		copilotOrg = app.Credentials.Owner
	}
	return githubCredentials{app: app, operator: githubToken, copilotOrg: copilotOrg}
}

// operatorToken is the person's token, for the path without an App.
func (c githubCredentials) operatorToken(ctx context.Context) string {
	if c.operator == nil {
		return githubToken(ctx)
	}
	return c.operator(ctx)
}

// refuseWrites stops a server reading as an App from being given anything to write: the App asks
// for no write, a dependency snapshot and default setup are the repository's own to change, and a
// write carried out with the operator's token would be the person's token in production.
func (c githubCredentials) refuseWrites(cfg serveConfig) error {
	if c.app == nil {
		return nil
	}
	for flag, value := range map[string]string{"--submit-targets": cfg.submitTargets,
		"--enable-targets": cfg.enableTargets, "--enable-accounts": cfg.enableAccounts} {
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("serve: %s names something to write, and a server reading as a GitHub App "+
				"writes nothing; drop it, or serve without the App to write with your own token", flag)
		}
	}
	return nil
}

// snapshotClient submits to targets, with the reader that resolves the commit a snapshot names. With
// an App it approves nothing and holds no token, so every submission is refused before GitHub is asked.
func (c githubCredentials) snapshotClient(ctx context.Context, apiURL string, targets []string,
) (*submit.Client, func(context.Context, string) (string, error)) {
	if c.app == nil {
		token := c.operatorToken(ctx)
		return submit.NewClient(token, targets), catalog.New(apiURL, token, nil).HeadSHA
	}
	return submit.NewClient("", nil), newReader(ctx, apiURL, c.app).HeadSHA
}

// patcher writes default setup to what allowed names, with the operator's token: a server reading as
// an App is never given anything to write.
func (c githubCredentials) patcher(ctx context.Context, apiURL string, allowed func(string) bool) *setup.Patcher {
	return &setup.Patcher{BaseURL: apiURL, Token: c.operatorToken(ctx), Allowed: allowed}
}

// copilot has sessions mint an installation token billed to the organization, where an App is
// configured and nothing else named a credential; otherwise they keep gh's.
func (c githubCredentials) copilot(o *authoringOptions) {
	if c.app == nil || o.token != nil {
		return
	}
	org := cmp.Or(o.org, c.copilotOrg)
	app := c.app
	o.credential = toolchain.Credential{Kind: toolchain.CredentialInstallation, Name: "Copilot credential",
		Source: "GitHub App installation token for " + org,
		Remedy: "an owner of " + org + " approves Copilot requests for the App, and the organization's " +
			"policy allows Copilot requests from GitHub App installations"}
	if org == "" {
		o.credential.Source = "GitHub App installation token"
		o.credential.Remedy = "name the organization Copilot requests are billed to with --authoring-org"
	}
	o.mint = func(ctx context.Context, refresh bool) (string, time.Time, error) {
		if org == "" {
			return "", time.Time{}, errors.New("the App is owned by a user, so no organization is billed for " +
				"Copilot requests; name one it is installed on with --authoring-org")
		}
		return app.TokenWith(ctx, org, ghapp.CopilotPermissions, refresh)
	}
	o.revoke = app.Revoke
}

// verify establishes which accounts may be declared: where the App is installed, or what gh reports.
func (c githubCredentials) verify() func(context.Context) (ghauth.Identity, error) {
	if c.app == nil {
		return func(ctx context.Context) (ghauth.Identity, error) { return ghauth.Verify(ctx, ghauth.Exec) }
	}
	return c.app.Identity
}

// roleReader reads the role behind a member's declared repository: through gh, or with an App,
// which has no person whose role to read, never.
func (c githubCredentials) roleReader() func(ctx context.Context, owner, repo, login string) (string, error) {
	if c.app == nil {
		return func(ctx context.Context, owner, repo, login string) (string, error) {
			return ghauth.ReadRepositoryRole(ctx, ghauth.Exec, owner, repo, login)
		}
	}
	return func(context.Context, string, string, string) (string, error) {
		return "", errors.New("with an App, eligibility is where it is installed, and no person's role is read")
	}
}

// picker lists an account's repositories and properties through the App; nil leaves the server's gh.
func (c githubCredentials) picker() (
	func(context.Context, string, bool) ([]ghauth.Repository, error),
	func(context.Context, string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error),
) {
	if c.app == nil {
		return nil, nil
	}
	list := func(ctx context.Context, owner string, _ bool) ([]ghauth.Repository, error) {
		return c.app.Repositories(ctx, owner)
	}
	return list, serve.ReadPropertiesWith(c.app.Runner())
}

// describeApp is the banner line saying which identity reads GitHub, and which one lends to owners without one.
func describeApp(app *ghapp.App) string {
	lender := "the earliest installation"
	if app.PublicReads != "" {
		lender = app.PublicReads + "'s installation"
	}
	return fmt.Sprintf("serve: reading GitHub as App %s (%d); owners without an installation are read through %s, "+
		"Copilot sessions carry tokens it mints for them, and nothing is written", app.Credentials.Slug, app.Credentials.AppID, lender)
}

// eventsRelayEnv names the relay channel outside argv, which make status prints; the channel reads every delivery.
const eventsRelayEnv = "ABOM_EVENTS_RELAY"

// relayTarget is this server's own receiver, reached over loopback whatever address it listens on.
func relayTarget(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + "/api/events/github"
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/events/github"
}

// startRelay relays the channel's deliveries to this server's receiver, which verifies each signature.
func startRelay(ctx context.Context, cfg serveConfig, log io.Writer) error {
	if cfg.eventsRelay == "" {
		return nil
	}
	if err := absoluteURL("--events-relay", cfg.eventsRelay); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if len(cfg.webhookSecret) == 0 {
		return errors.New("serve: a relay needs a webhook secret to verify what it relays: " +
			"configure an App, --webhook-secret-file or $" + webhookSecretEnv)
	}
	fmt.Fprintln(log, "serve: relaying webhook deliveries; deliveries sent while this server is down are lost, "+
		"and the recheck sweep is what notices those changes")
	go func() { _ = (&ghapp.Relay{Source: cfg.eventsRelay, Target: relayTarget(cfg.addr), Log: log}).Run(ctx) }()
	return nil
}
