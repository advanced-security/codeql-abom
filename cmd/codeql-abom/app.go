package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const projectURL = "https://github.com/advanced-security/codeql-abom"

// runApp creates, checks and rotates the read-only GitHub App, and records its installations.
func runApp(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	actions := map[string]func(context.Context, []string, io.Writer, io.Writer) error{
		"create": runAppCreate, "status": runAppStatus, "installations": runAppInstallations,
		"rotate-key": runAppRotateKey,
	}
	if len(args) > 0 {
		if action, ok := actions[args[0]]; ok {
			return action(ctx, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, "Usage: codeql-abom app <create|status|installations|rotate-key> [flags]")
	if len(args) == 0 {
		return errors.New("app: missing action")
	}
	return fmt.Errorf("app: unknown action %q", args[0])
}

// appDir resolves the App directory: named outright, or beside the database it serves.
func appDir(fs *flag.FlagSet) func() (string, error) {
	dir := fs.String("dir", "", "directory holding the App's identity and key (default: github-app beside --db)")
	db := fs.String("db", "", "ABOM database the App directory sits beside")
	return func() (string, error) {
		switch {
		case *dir != "":
			return *dir, nil
		case *db != "":
			return ghapp.DefaultDir(*db), nil
		}
		return "", errors.New("name the App directory with --dir, or the database it sits beside with --db")
	}
}

func runAppCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("app create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := appDir(fs)
	owner := fs.String("owner", "", "account to create the App under (required)")
	user := fs.Bool("user", false, "create it under your personal account rather than an organization")
	relay := fs.String("events-relay", "", "relay channel GitHub delivers to, such as https://smee.io/<channel>")
	public := fs.String("public-url", "", "address GitHub reaches this ABOM at; deliveries go to its /api/events/github")
	name := fs.String("name", "", "the App's name on GitHub (default: abom-<owner>)")
	webURL := fs.String("web-url", "https://github.com", "GitHub web address")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	addr := fs.String("addr", "127.0.0.1:0", "loopback address to serve the setup page on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := resolve()
	if err != nil {
		return fmt.Errorf("app create: %w", err)
	}
	webhook, homepage, err := deliveryAddress(*relay, *public)
	if err != nil {
		return fmt.Errorf("app create: %w", err)
	}
	if *owner == "" {
		return errors.New("app create: name the account to create the App under with --owner")
	}
	if ghapp.Configured(dir) {
		return fmt.Errorf("app create: an App is already configured in %s; move it aside to create another", dir)
	}
	if host, _, err := net.SplitHostPort(*addr); err != nil || !isLoopbackHost(host) {
		return fmt.Errorf("app create: --addr %s is not a loopback address; the setup page is served on loopback only", *addr)
	}
	kind := "organization"
	if *user {
		kind = "user"
	}
	appName := *name
	if appName == "" {
		appName = "abom-" + *owner
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("app create: %w", err)
	}
	base := "http://" + ln.Addr().String()
	setup := &ghapp.Setup{
		Owner: *owner, OwnerKind: kind, Name: appName, Homepage: homepage, WebhookURL: webhook,
		WebURL: *webURL, APIURL: *apiURL, Dir: dir,
		RedirectURL: base + "/callback", Created: make(chan ghapp.Credentials, 1),
	}
	srv := &http.Server{Handler: setup.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	fmt.Fprintf(stdout, "Open %s/ in your browser. It lists every permission the App requests and why.\n", base)
	fmt.Fprintln(stdout, "Nothing is created until you click Create GitHub App on GitHub's page. Waiting up to an hour.")
	select {
	case creds := <-setup.Created:
		printCreated(stdout, creds, dir, *relay)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Hour):
		return errors.New("app create: no App was created within the hour GitHub allows for the flow")
	}
}

// deliveryAddress is the relay when one is named, otherwise this ABOM's public receiver.
func deliveryAddress(relay, public string) (webhook, homepage string, err error) {
	homepage = projectURL
	if public != "" {
		if err := absoluteURL("--public-url", public); err != nil {
			return "", "", err
		}
		homepage = strings.TrimSuffix(public, "/")
	}
	switch {
	case relay != "":
		if err := absoluteURL("--events-relay", relay); err != nil {
			return "", "", err
		}
		return relay, homepage, nil
	case public != "":
		return homepage + "/api/events/github", homepage, nil
	}
	return "", "", errors.New("name where GitHub delivers events: --events-relay for a relay channel, " +
		"or --public-url for an address GitHub can reach")
}

func absoluteURL(flagName, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%s %q is not an absolute http(s) URL", flagName, raw)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func printCreated(w io.Writer, c ghapp.Credentials, dir, relay string) {
	fmt.Fprintf(w, "\nCreated %s (App %d) under %s, saved in %s:\n", c.Slug, c.AppID, c.Owner, dir)
	fmt.Fprintf(w, "  private key     %s (0600, %s)\n", ghapp.KeyFile(dir), ghapp.Fingerprint(&c.Key.PublicKey))
	fmt.Fprintf(w, "  webhook secret  %s (0600)\n", ghapp.WebhookSecretFile(dir))
	fmt.Fprintln(w, "Next:")
	fmt.Fprintf(w, "  1. Install it, choosing only the repositories ABOM should read: %s/installations/new\n",
		strings.TrimSuffix(c.HTMLURL, "/"))
	fmt.Fprintln(w, "  2. Serve as the App: serve finds it beside --db, uses its webhook secret, and records its installations.")
	if relay != "" {
		fmt.Fprintln(w, "     Relay deliveries to it with: ABOM_EVENTS_RELAY="+relay+" codeql-abom serve --db <abom.db>")
	}
}

func runAppStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("app status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := appDir(fs)
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := resolve()
	if err != nil {
		return fmt.Errorf("app status: %w", err)
	}
	creds, err := ghapp.Load(dir)
	if err != nil {
		return fmt.Errorf("app status: %w", err)
	}
	fmt.Fprintf(stdout, "App             %s (%d), owned by %s\n", creds.Slug, creds.AppID, creds.Owner)
	fmt.Fprintf(stdout, "Settings        %s\n", creds.HTMLURL)
	fmt.Fprintf(stdout, "Deliveries to   %s\n", creds.WebhookURL)
	fmt.Fprintf(stdout, "Private key     %s %s\n", ghapp.KeyFile(dir), ghapp.Fingerprint(&creds.Key.PublicKey))
	if _, err := ghapp.WebhookSecret(dir); err != nil {
		fmt.Fprintf(stdout, "Webhook secret  unusable: %v\n", err)
	} else {
		fmt.Fprintf(stdout, "Webhook secret  %s\n", ghapp.WebhookSecretFile(dir))
	}
	if err := (&ghapp.App{Credentials: creds, API: *apiURL}).Verify(ctx); err != nil {
		fmt.Fprintf(stdout, "GitHub          does not accept this key: %v\n", err)
		return fmt.Errorf("app status: %w", err)
	}
	fmt.Fprintln(stdout, "GitHub          accepts this key")
	return nil
}

func runAppInstallations(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("app installations", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirFlag := fs.String("dir", "", "directory holding the App's identity and key (default: github-app beside --db)")
	dbPath := fs.String("db", "", "ABOM database to record the installations in (required)")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return errors.New("app installations: name the database to record them in with --db")
	}
	dir := *dirFlag
	if dir == "" {
		dir = ghapp.DefaultDir(*dbPath)
	}
	creds, err := ghapp.Load(dir)
	if err != nil {
		return fmt.Errorf("app installations: %w", err)
	}
	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("app installations: %w", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("app installations: %w", err)
	}

	sync := (&ghapp.App{Credentials: creds, API: *apiURL}).ReadInstallations(ctx)
	if err := s.RecordInstallationSync(sync); err != nil {
		return fmt.Errorf("app installations: %w", err)
	}
	if sync.Unread != "" {
		fmt.Fprintf(stdout, "The App's installations could not be read, so every account stays unknown: %s\n", sync.Unread)
	}
	for _, inst := range sync.Installations {
		fmt.Fprintf(stdout, "%s (%s), installation %d: %s\n", inst.Owner, inst.Kind, inst.InstallationID, selection(inst))
	}
	declared, err := s.OwnedScope()
	if err != nil {
		return fmt.Errorf("app installations: %w", err)
	}
	owners := make([]string, 0, len(declared))
	for _, a := range declared {
		owners = append(owners, a.Owner)
	}
	evidence, err := s.InstallationEvidence(owners)
	if err != nil {
		return fmt.Errorf("app installations: %w", err)
	}
	fmt.Fprintln(stdout, "\nBeside each declared account (an installation is evidence, and changes no declaration):")
	for _, owner := range owners {
		e := evidence[strings.ToLower(owner)]
		if e == nil {
			continue
		}
		reason := ""
		if e.Reason != "" {
			reason = " (" + e.Reason + ")"
		}
		fmt.Fprintf(stdout, "  %s: %s%s\n", owner, e.State, reason)
	}
	if sync.Unread != "" {
		return errors.New("app installations: the App's installations could not be read")
	}
	return nil
}

func selection(inst store.AppInstallation) string {
	switch {
	case inst.Suspended:
		return "suspended"
	case inst.RepositorySelection == "all":
		return "every repository"
	case inst.RepositoriesUnread != "":
		return "selected repositories, unread: " + inst.RepositoriesUnread
	}
	return "selected: " + strings.Join(inst.Repositories, ", ")
}

func runAppRotateKey(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("app rotate-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := appDir(fs)
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	keyPath := fs.String("key", "", "the new private key, as downloaded from the App's settings (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := resolve()
	if err != nil {
		return fmt.Errorf("app rotate-key: %w", err)
	}
	if *keyPath == "" {
		return errors.New("app rotate-key: name the new key with --key")
	}
	pemBytes, err := os.ReadFile(*keyPath)
	if err != nil {
		return fmt.Errorf("app rotate-key: %w", err)
	}
	rotated, err := ghapp.RotateKey(ctx, dir, pemBytes, func(ctx context.Context, c ghapp.Credentials) error {
		return (&ghapp.App{Credentials: c, API: *apiURL}).Verify(ctx)
	})
	if err != nil {
		return fmt.Errorf("app rotate-key: %w", err)
	}
	creds, err := ghapp.Load(dir)
	if err != nil {
		return fmt.Errorf("app rotate-key: %w", err)
	}
	settings := "https://github.com/settings/apps/" + creds.Slug
	if creds.OwnerKind != "user" {
		settings = "https://github.com/organizations/" + creds.Owner + "/settings/apps/" + creds.Slug
	}
	fmt.Fprintf(stdout, "Installed %s in %s.\n", rotated.Installed, ghapp.KeyFile(dir))
	fmt.Fprintf(stdout, "Next: restart serve to load it, then delete %s under Private keys at %s,\n", rotated.Previous, settings)
	fmt.Fprintf(stdout, "and delete the downloaded copy: %s\n", *keyPath)
	return nil
}
