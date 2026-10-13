// Package demo is a Demo instance's practice data: a marked workspace built from the fictional
// corpus, and the loopback-only provider that serves its repositories.
package demo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/corpus"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Scenario is the version of the practice data this build initializes and accepts.
const Scenario = "corpus-1"

const manifestFormat = 1

// Branch is every demo repository's default branch.
const Branch = "main"

// baselineAt dates every demo commit, so the same sources are the same commits in every Codespace.
const baselineAt = "2026-01-01T00:00:00Z"

// Layout places everything a Demo instance writes under one root.
type Layout struct{ Root string }

func (l Layout) Manifest() string  { return filepath.Join(l.Root, "demo.json") }
func (l Layout) Database() string  { return filepath.Join(l.Root, "abom.db") }
func (l Layout) Pack() string      { return filepath.Join(l.Root, "ql") }
func (l Layout) Work() string      { return filepath.Join(l.Root, "work") }
func (l Layout) Databases() string { return filepath.Join(l.Root, "databases") }
func (l Layout) Git() string       { return filepath.Join(l.Root, "git") }

// Secrets holds the Copilot credential a user supplies after creation, and nothing else.
func (l Layout) Secrets() string { return filepath.Join(l.Root, "secrets") }

// Repository is the bare repository the provider serves for owner/name.
func (l Layout) Repository(name string) string {
	return filepath.Join(l.Git(), filepath.FromSlash(name)+".git")
}

// Repository is one fictional repository and the commit its default branch holds.
type Repository struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// Manifest is written last, so a workspace without one is an interrupted initialization.
type Manifest struct {
	Format       int          `json:"format"`
	Dataset      string       `json:"dataset"`
	Scenario     string       `json:"scenario"`
	CreatedAt    string       `json:"createdAt"`
	Repositories []Repository `json:"repositories"`
}

// Has reports whether the dataset names a repository.
func (m Manifest) Has(name string) (Repository, bool) {
	for _, r := range m.Repositories {
		if r.Name == name {
			return r, true
		}
	}
	return Repository{}, false
}

// InitOptions name one initialization.
type InitOptions struct {
	Root string
	// Sources is the corpus: fixture source trees beside their recorded CodeQL output.
	Sources string
	// Git is the git executable; empty finds it on PATH.
	Git string
	// Replace starts again over an existing workspace, and only over one.
	Replace bool
	// PackLocks holds the lock files provisioning resolved for the query pack and its test pack,
	// at codeql-pack.lock.yml and test/codeql-pack.lock.yml; empty leaves the pack unlocked.
	PackLocks string
	Out       io.Writer
}

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Init creates a fresh workspace with a new dataset identity.
func Init(ctx context.Context, o InitOptions) error {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	if o.Sources, err = filepath.Abs(o.Sources); err != nil {
		return err
	}
	if err := prepareRoot(root, o.Replace); err != nil {
		return err
	}
	l := Layout{Root: root}
	git := o.Git
	if git == "" {
		if git, err = exec.LookPath("git"); err != nil {
			return errors.New("initializing a demo needs git, which is not installed")
		}
	}
	fixtures := filepath.Join(o.Sources, "fixtures")
	names, err := corpus.Repositories(fixtures)
	if err != nil {
		return fmt.Errorf("reading the demo sources: %w", err)
	}
	m := Manifest{
		Format: manifestFormat, Dataset: "demo-" + strings.ToLower(rand.Text()[:16]), Scenario: Scenario,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.Repositories = make([]Repository, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	slots := make(chan struct{}, max(1, runtime.NumCPU()))
	for i, name := range names {
		if !repositoryName.MatchString(name) || strings.Contains(name, "..") {
			return fmt.Errorf("the demo sources name %q, which is not a repository", name)
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			commit, err := commitBaseline(ctx, git, l.Repository(name), filepath.Join(o.Sources, filepath.FromSlash(name)))
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", name, err)
			}
			m.Repositories[i] = Repository{Name: name, Branch: Branch, Commit: commit}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := corpus.Build(corpus.Options{
		Database: l.Database(), Fixtures: fixtures, Queries: l.Pack(), Out: o.Out,
		Demo: &store.DemoDataset{ID: m.Dataset, Scenario: m.Scenario},
	}); err != nil {
		return fmt.Errorf("building the demo database: %w", err)
	}
	if err := recordPractitioner(l.Database(), m); err != nil {
		return err
	}
	if o.PackLocks != "" {
		if err := copyPackLocks(o.PackLocks, l.Pack()); err != nil {
			return err
		}
	}
	for _, dir := range []string{l.Work(), l.Databases(), l.Secrets()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return writeManifest(l.Manifest(), m)
}

// copyPackLocks gives both packs the locks provisioning resolved, so nothing installs at run time.
func copyPackLocks(from, pack string) error {
	for _, rel := range []string{"codeql-pack.lock.yml", filepath.Join("test", "codeql-pack.lock.yml")} {
		src := filepath.Join(from, rel)
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("the provisioned pack locks have no %s", rel)
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pack, rel), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Practitioner is the fictional person a Demo's requests and sessions are recorded as filed by.
const Practitioner = "demo-practitioner"

// recordPractitioner stands in for the identity an ordinary instance reads from gh, which a Demo
// never reads: it administers every fictional account, and its host cannot resolve.
func recordPractitioner(db string, m Manifest) error {
	s, err := store.OpenBackend(db, store.Options{Demo: true})
	if err != nil {
		return err
	}
	defer s.Close()
	v := store.Verification{Login: Practitioner, Host: "github.demo.invalid", VerifiedAt: time.Now().UTC()}
	seen := map[string]bool{}
	for _, r := range m.Repositories {
		owner, _, _ := strings.Cut(r.Name, "/")
		if !seen[owner] {
			seen[owner] = true
			v.Accounts = append(v.Accounts, store.Account{Owner: owner, Kind: "organization", Role: store.RoleAdmin})
		}
	}
	if err := s.ReplaceAccounts(v); err != nil {
		return fmt.Errorf("recording the demo practitioner: %w", err)
	}
	return nil
}

// prepareRoot refuses anything but an absent or empty directory, or a demo being replaced.
func prepareRoot(root string, replace bool) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(root, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory a demo can be created in", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if !replace {
		return fmt.Errorf("%s is not empty; a demo is created only in an empty directory, "+
			"or replaced with --replace when it already is one", root)
	}
	if _, err := readManifest(Layout{Root: root}.Manifest()); err != nil {
		return fmt.Errorf("refusing to replace %s, which is not a demo workspace: %w", root, err)
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.MkdirAll(root, 0o700)
}

// commitBaseline makes a bare repository whose default branch is one commit of the source tree.
func commitBaseline(ctx context.Context, git, bare, source string) (string, error) {
	if info, err := os.Lstat(source); err != nil || !info.IsDir() {
		return "", fmt.Errorf("no source tree at %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(bare), 0o700); err != nil {
		return "", err
	}
	run := func(dir string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir, cmd.Env = dir, gitEnvironment()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	tree := []string{"--git-dir", bare, "--work-tree", source}
	steps := [][]string{
		{"init", "--quiet", "--bare", "--initial-branch", Branch, bare},
		append(tree, "add", "--all", "."),
		append(tree, "-c", "core.hooksPath="+os.DevNull, "commit", "--quiet", "--no-gpg-sign",
			"--message", "Demo baseline"),
	}
	for _, args := range steps {
		if _, err := run(source, args...); err != nil {
			return "", err
		}
	}
	// The index belongs to the work tree used to commit, which a bare repository does not have.
	if err := os.Remove(filepath.Join(bare, "index")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	config, err := os.OpenFile(filepath.Join(bare, "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return "", err
	}
	// Read-only service: partial clones and fetch by commit, and never a push.
	_, err = config.WriteString("[uploadpack]\n\tallowFilter = true\n\tallowAnySHA1InWant = true\n" +
		"[http]\n\treceivepack = false\n")
	if err := errors.Join(err, config.Close()); err != nil {
		return "", err
	}
	return run(source, "--git-dir", bare, "rev-parse", "HEAD")
}

// gitEnvironment is named, not inherited: no operator configuration, credential helper or signing
// key takes part in building or serving practice data.
func gitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=ABOM Demo", "GIT_AUTHOR_EMAIL=demo@abom.invalid", "GIT_AUTHOR_DATE=" + baselineAt,
		"GIT_COMMITTER_NAME=ABOM Demo", "GIT_COMMITTER_EMAIL=demo@abom.invalid", "GIT_COMMITTER_DATE=" + baselineAt,
	}
}

func writeManifest(path string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("no demo manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, errors.New("the demo manifest is not a regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("reading the demo manifest: %w", err)
	}
	if m.Format != manifestFormat || m.Dataset == "" {
		return Manifest{}, fmt.Errorf("the demo manifest is format %d, and this build reads %d", m.Format, manifestFormat)
	}
	return m, nil
}

// Workspace is a validated demo: its manifest and the marked database that agrees with it.
type Workspace struct {
	Layout   Layout
	Manifest Manifest
	Store    store.Backend
}

func (w Workspace) Close() error {
	if w.Store == nil {
		return nil
	}
	return w.Store.Close()
}

// Open validates a workspace before anything serves it.
func Open(root string) (Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, err
	}
	l := Layout{Root: abs}
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Workspace{}, fmt.Errorf("%s is not a demo workspace; create one with `codeql-abom demo init`", abs)
	}
	m, err := readManifest(l.Manifest())
	if err != nil {
		return Workspace{}, fmt.Errorf("%s: %w; it was not initialized completely, so initialize it again", abs, err)
	}
	if m.Scenario != Scenario {
		return Workspace{}, fmt.Errorf("%s holds scenario %s, and this build serves %s; initialize it again",
			abs, m.Scenario, Scenario)
	}
	if err := confined(l, m); err != nil {
		return Workspace{}, err
	}
	s, err := store.OpenBackend(l.Database(), store.Options{Demo: true})
	if err != nil {
		return Workspace{}, err
	}
	d, marked, err := s.DemoDataset()
	if err == nil && (!marked || d.ID != m.Dataset || d.Scenario != m.Scenario) {
		err = fmt.Errorf("the database in %s is not the dataset its manifest names", abs)
	}
	if err != nil {
		_ = s.Close()
		return Workspace{}, err
	}
	return Workspace{Layout: l, Manifest: m, Store: s}, nil
}

// confined refuses a workspace any of whose parts is a link out of it.
func confined(l Layout, m Manifest) error {
	parts := map[string]bool{l.Database(): false, l.Pack(): true, l.Work(): true,
		l.Databases(): true, l.Git(): true, l.Secrets(): true}
	for _, r := range m.Repositories {
		if !repositoryName.MatchString(r.Name) || strings.Contains(r.Name, "..") {
			return fmt.Errorf("the demo manifest names %q, which is not a repository", r.Name)
		}
		parts[l.Repository(r.Name)] = true
	}
	for path, dir := range parts {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("the demo workspace has no %s; initialize it again", filepath.Base(path))
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != dir {
			return fmt.Errorf("%s is not part of the demo workspace", path)
		}
	}
	return nil
}
