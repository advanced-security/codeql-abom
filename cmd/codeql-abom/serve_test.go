package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// A database migrated in place and never crawled carries no boundary on any edge, because the
// only thing that classifies them runs inside a discovery cycle. Every boundary filter then
// answers nothing, which reads as "no cross-owner dependencies" rather than "never measured".
func TestServingClassifiesEdgesNoCycleHasClassified(t *testing.T) {
	db := prunableDB(t)
	if unclassifiedEdgeCount(t, db) == 0 {
		t.Fatal("fixture arrives classified, so this test would pass against an empty change")
	}

	serveUntilOpened(t, db)

	if n := unclassifiedEdgeCount(t, db); n != 0 {
		t.Errorf("%d edges unclassified after serving; a boundary filter would answer nothing", n)
	}
}

// A crawl that was running is remembered, and a restart does not act on the memory unless the
// operator asked it to. Resuming on its own spends rate limit, disk and every core on a decision
// nobody took at that moment.
func TestARunningCrawlIsNotResumedUnlessResumingWasConfigured(t *testing.T) {
	s := openStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if shouldResume(s, false, &out, &out) {
		t.Error("discovery resumed without anybody configuring it to")
	}
	if !strings.Contains(out.String(), "left paused") {
		t.Errorf("a crawl that stopped resuming says nothing about it: %q", out.String())
	}
}

// Configured, it resumes — otherwise the preference is a control that does nothing.
func TestAConfiguredCrawlResumes(t *testing.T) {
	s := openStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDiscoveryResumeOnStart(true); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if !shouldResume(s, false, &out, &out) {
		t.Error("resume-on-start is set and the crawl did not resume")
	}
}

// The preference is about resuming, never about starting: an installation that was paused, or
// that nobody ever started, stays stopped however the preference is set.
func TestResumeOnStartDoesNotStartACrawlNobodyStarted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent func(*store.Store) error
	}{
		{"never started", func(*store.Store) error { return nil }},
		{"paused", func(s *store.Store) error { return s.SetDiscoveryIntent(false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			if err := tc.intent(s); err != nil {
				t.Fatal(err)
			}
			if err := s.SetDiscoveryResumeOnStart(true); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if shouldResume(s, false, &out, &out) {
				t.Error("a crawl started that the operator had not started")
			}
		})
	}
}

// `--expand` is somebody asking on this invocation, which is explicit enough on its own.
func TestExpandStartsDiscoveryWhateverIsRecorded(t *testing.T) {
	s := openStore(t)

	var out bytes.Buffer
	if !shouldResume(s, true, &out, &out) {
		t.Error("--expand did not start discovery")
	}
}

// The loop learns of a declaration from the store every writer shares, so one saved in Settings,
// with `own`, or by another replica is read on the next cycle rather than when the period comes round.
func TestTheLoopTellsOneDeclarationFromTheNext(t *testing.T) {
	s := openStore(t)
	loop := &supervisor.Supervisor{}
	wireEnumeration(loop, &serve.Server{}, s, time.Hour)
	declare := func(accounts ...store.DeclaredAccount) (string, []string) {
		t.Helper()
		if err := s.SetOwnedScope(accounts, store.DeclaredByCLI); err != nil {
			t.Fatal(err)
		}
		version, owners, err := loop.Declared()
		if err != nil {
			t.Fatal(err)
		}
		return version, owners
	}
	whole := store.DeclaredAccount{Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true}
	narrowed := whole
	narrowed.IncludesOnly, narrowed.Includes = true, []string{"one"}

	before, _ := declare(whole)
	after, owners := declare(narrowed, store.DeclaredAccount{Owner: "octo", Kind: store.AccountUser, Enabled: true})

	if before == "" || before == after {
		t.Errorf("the two declarations read as %q and %q, which the loop cannot tell apart", before, after)
	}
	if slices.Sort(owners); strings.Join(owners, ",") != "has-ghas,octo" {
		t.Errorf("owners = %v, want both accounts the declaration names", owners)
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return s
}

// serveUntilOpened runs the open-time work and stops. The context is canceled before the call,
// so serve reaches its shutdown select immediately rather than waiting for a signal that a test
// has no way to send.
//
// The writer is locked because serve hands it to the goroutine that prints the listening banner
// and returns without waiting for it: reading a plain bytes.Buffer afterwards is a race, and the
// detector finds it every run.
func serveUntilOpened(t *testing.T, db string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := &lockedWriter{}
	if err := runServe(ctx, []string{"--db", db, "--addr", "127.0.0.1:0"}, out, out); err != nil {
		t.Fatalf("serve --db %s: %v (%s)", db, err, out.String())
	}
}

type lockedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func unclassifiedEdgeCount(t *testing.T, db string) int {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_edge_live WHERE boundary IS NULL`).Scan(&n); err != nil {
		t.Fatalf("counting unclassified edges: %v", err)
	}
	return n
}

// The crawl recognizes a moved reference's content from one tree read, and a read that would wait
// out a rate limit is refused rather than retried: the clone it falls back to costs less.
func TestTheCrawlReadsTreesWithoutWaitingOutARateLimit(t *testing.T) {
	loop := &supervisor.Supervisor{Expander: &expand.Expander{}}

	wireTrees(context.Background(), loop, "https://api.example.test", nil)

	trees, ok := loop.Expander.Trees.(expand.GitHubTrees)
	if !ok || trees.Client == nil {
		t.Fatalf("the crawl reads no trees: %#v", loop.Expander.Trees)
	}
	if trees.Client.MaxRetries != 0 {
		t.Errorf("a tree read retries %d times; it should fall back to the clone", trees.Client.MaxRetries)
	}
}

// Every details page draws with Graphviz, so a server without it is a broken installation: it
// refuses to start and names the remedy rather than serving pages that cannot draw.
func TestServeRefusesToStartWithoutGraphviz(t *testing.T) {
	db := filepath.Join(t.TempDir(), "abom.db")
	t.Setenv("PATH", t.TempDir())
	// Bounded, so a server that wrongly starts stops and fails the test rather than hanging it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := &lockedWriter{}
	err := runServe(ctx, []string{"--db", db, "--addr", "127.0.0.1:0"}, out, out)
	if err == nil {
		t.Fatalf("serve started without Graphviz: %s", out.String())
	}
	if !strings.Contains(err.Error(), "install Graphviz") {
		t.Errorf("serve refused without naming the remedy: %v", err)
	}
}

// The banner names the Graphviz it draws with, which is what a figure's bytes depend on.
func TestServeNamesTheGraphvizItDrawsWith(t *testing.T) {
	version, err := layout.Version(context.Background())
	if err != nil {
		t.Fatalf("Graphviz is required to run these tests: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := &lockedWriter{}
	db := filepath.Join(t.TempDir(), "abom.db")
	if err := runServe(ctx, []string{"--db", db, "--addr", "127.0.0.1:0"}, out, out); err != nil {
		t.Fatalf("serve: %v (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "Graphviz "+version) {
		t.Errorf("the banner does not name Graphviz %s:\n%s", version, out.String())
	}
}

// A session runs its CLIs in the pack and confines the agent to paths read against it, so a path
// flag named relative to where the server started must reach neither relative: it is made absolute
// once, at parse, and nobody has to know to pass absolute paths.
func TestPathFlagsAreNamedFromWhereTheServerStarted(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := parseServeArgs([]string{"--db", "abom.db", "--custom-ql-dir", "ql/custom", "--work-dir", "work",
		"--cache-dir", "cache", "--database-cache-dir", "databases", "--codeql", "bin/codeql"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.discovery
	for name, got := range map[string]string{"--custom-ql-dir": d.customQLDir, "--work-dir": d.workDir,
		"--cache-dir": d.cacheDir, "--database-cache-dir": d.databaseCacheDir, "--codeql": d.codeqlPath} {
		if !filepath.IsAbs(got) {
			t.Errorf("%s reached the server as %q", name, got)
		}
	}
	bare, err := parseServeArgs([]string{"--db", "abom.db", "--codeql", "codeql"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if bare.discovery.codeqlPath != "codeql" || bare.discovery.workDir != "" {
		t.Errorf("a bare name or an absent flag changed: codeql %q, work dir %q",
			bare.discovery.codeqlPath, bare.discovery.workDir)
	}
}

// The local file seeds a fresh database for convenience. Refusing what it declares because of the
// role on record must not take the interface down with it: the interface is where a declaration
// the role supports is made. A file that cannot be a declaration at all still stops the server.
func TestALocalDeclarationTheRoleCannotSupportIsSkippedAndServingGoesOn(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "abom.db")
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAccounts(store.Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Accounts: []store.Account{{Owner: "acme", Kind: store.AccountOrganization, Role: store.RoleMember,
			SecurityManager: "no"}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	file := filepath.Join(dir, "abom.local.json")
	if err := os.WriteFile(file, []byte(`{"own": {"orgs": ["acme"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	served, err := openServeStore(serveConfig{dbPath: db}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	if err := seedDeclaration(t.Context(), served, file, parityRole, &stderr); err != nil {
		t.Fatalf("a declaration the role cannot support stopped the server: %v", err)
	}
	declared, err := served.OwnedScope()
	if err != nil || len(declared) != 0 {
		t.Errorf("the refused declaration was written anyway: %+v, %v", declared, err)
	}
	if !strings.Contains(stderr.String(), file) || !strings.Contains(stderr.String(), "administer or maintain") {
		t.Errorf("serving went on without saying the file was refused, or why:\n%s", stderr.String())
	}

	if err := os.WriteFile(file, []byte(`{"own": {"users": ["acme"], "properties": [{"org": "acme", "match": [{"name": "team", "value": "x"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedDeclaration(t.Context(), served, file, parityRole, io.Discard); err == nil {
		t.Error("a file that cannot be a declaration at all no longer stops the server")
	}
}

// serve's flags are the CLI a deployment is written against, so a rename, a dropped flag or a new
// default shows here and is changed on purpose. testdata/serve-help.txt is that surface.
func TestServesFlagsAndTheirHelpChangeOnlyOnPurpose(t *testing.T) {
	for _, name := range []string{"ABOM_DB", "ABOM_READ_REPLICA", "ABOM_WORKER_IMAGE", "COPILOT_CLI_PATH", "GITHUB_API_URL"} {
		t.Setenv(name, "")
	}
	want, err := os.ReadFile(filepath.Join("testdata", "serve-help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := parseServeArgs([]string{"-help"}, &got); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("serve -help: %v", err)
	}
	// The ql-mcp pin moves with every CodeQL upgrade, and check-versions holds it to the CLI.
	shown := qlmcpPin.ReplaceAllString(got.String(), "${1}<pinned>")
	if shown != string(want) {
		t.Errorf("serve -help differs from testdata/serve-help.txt:\n%s", shown)
	}
}

var qlmcpPin = regexp.MustCompile(`(codeql-development-mcp-server@)[0-9][^" ]*`)
