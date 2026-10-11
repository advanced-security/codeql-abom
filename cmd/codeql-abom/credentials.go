package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/store"
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

// dbReaderTokens is the token source for a command naming a database: the App beside it, or nil for the operator's token.
func dbReaderTokens(dbPath string) (ghapp.TokenSource, error) {
	app, err := readerApp(ghapp.DefaultDir(dbPath), "", defaultAPIURL())
	return readerTokens(app), err
}

// describeApp is the banner line saying which identity reads GitHub, and which one lends to owners without one.
func describeApp(app *ghapp.App) string {
	lender := "the earliest installation"
	if app.PublicReads != "" {
		lender = app.PublicReads + "'s installation"
	}
	return fmt.Sprintf("serve: reading GitHub as App %s (%d); owners without an installation are read through %s, "+
		"and dependency submission keeps your own token", app.Credentials.Slug, app.Credentials.AppID, lender)
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
