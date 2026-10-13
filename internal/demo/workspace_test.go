package demo

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

var corpusSources = filepath.Join("..", "..", "testdata", "corpus")

func initialized(t *testing.T) Workspace {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "demo")
	if err := Init(t.Context(), InitOptions{Root: root, Sources: corpusSources}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ws, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

var commitID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// A workspace is only Demo if its database and manifest agree, and every repository it names is
// one the simulated provider can actually serve.
func TestInitCreatesAMarkedWorkspaceWhoseRepositoriesCanBeServed(t *testing.T) {
	ws := initialized(t)
	got, marked, err := ws.Store.DemoDataset()
	if err != nil || !marked || got.ID != ws.Manifest.Dataset || got.Scenario != Scenario {
		t.Fatalf("marker %+v %v %v disagrees with manifest %+v", got, marked, err, ws.Manifest)
	}
	if len(ws.Manifest.Repositories) == 0 {
		t.Fatal("the manifest names no repositories")
	}
	for _, r := range ws.Manifest.Repositories {
		if !commitID.MatchString(r.Commit) || r.Branch == "" {
			t.Errorf("%s has commit %q on %q", r.Name, r.Commit, r.Branch)
		}
		out, err := exec.Command("git", "--git-dir", ws.Layout.Repository(r.Name), "rev-parse", "HEAD").Output()
		if err != nil || len(out) < 40 || string(out[:40]) != r.Commit {
			t.Errorf("%s: bare repository HEAD %q, %v; manifest says %s", r.Name, out, err, r.Commit)
		}
	}
	if s, err := store.OpenBackend(ws.Layout.Database(), store.Options{}); !errors.Is(err, store.ErrDemoDatabase) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("an ordinary open of the demo database: %v", err)
	}
}

// A new Codespace starts a new dataset, but the fictional repositories must be the same commits so
// a database built from one baseline is the same evidence as one built from the next.
func TestEachInitializationIsANewDatasetOverTheSameCommits(t *testing.T) {
	a, b := initialized(t), initialized(t)
	if a.Manifest.Dataset == b.Manifest.Dataset {
		t.Fatal("two initializations share a dataset identity, so browser progress would carry over")
	}
	if len(a.Manifest.Repositories) != len(b.Manifest.Repositories) {
		t.Fatal("two initializations name different repositories")
	}
	for i := range a.Manifest.Repositories {
		if a.Manifest.Repositories[i] != b.Manifest.Repositories[i] {
			t.Errorf("%+v != %+v", a.Manifest.Repositories[i], b.Manifest.Repositories[i])
		}
	}
}

// An operator's directory is never adopted or emptied by a Demo initialization.
func TestInitRefusesADirectoryItDidNotCreate(t *testing.T) {
	root := t.TempDir()
	mine := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(mine, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context(), InitOptions{Root: root, Sources: corpusSources}); err == nil {
		t.Fatal("Init wrote into a non-empty directory")
	}
	if err := Init(t.Context(), InitOptions{Root: root, Sources: corpusSources, Replace: true}); err == nil {
		t.Fatal("Init replaced a directory that holds no demo manifest")
	}
	if b, err := os.ReadFile(mine); err != nil || string(b) != "mine" {
		t.Fatalf("the operator's file is %q, %v", b, err)
	}
}

// Replacing is how a Codespace starts fresh; it applies only to a directory that is already a demo.
func TestReplacingAWorkspaceIssuesANewDataset(t *testing.T) {
	ws := initialized(t)
	before := ws.Manifest.Dataset
	_ = ws.Close()
	if err := Init(t.Context(), InitOptions{Root: ws.Layout.Root, Sources: corpusSources}); err == nil {
		t.Fatal("Init over an existing workspace without Replace was accepted")
	}
	if err := Init(t.Context(), InitOptions{Root: ws.Layout.Root, Sources: corpusSources, Replace: true}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	again, err := Open(ws.Layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Manifest.Dataset == before {
		t.Fatal("a replaced workspace kept its dataset identity")
	}
}

// Each of these is a workspace a Demo server must refuse rather than serve as practice data.
func TestOpenRefusesWorkspacesThatAreNotWhole(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, l Layout){
		"interrupted before the manifest": func(t *testing.T, l Layout) {
			if err := os.Remove(l.Manifest()); err != nil {
				t.Fatal(err)
			}
		},
		"manifest naming another dataset": func(t *testing.T, l Layout) {
			var m Manifest
			b, _ := os.ReadFile(l.Manifest())
			_ = json.Unmarshal(b, &m)
			m.Dataset = "someone-else"
			b, _ = json.Marshal(m)
			if err := os.WriteFile(l.Manifest(), b, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"database replaced by a link": func(t *testing.T, l Layout) {
			elsewhere := filepath.Join(t.TempDir(), "abom.db")
			if err := os.Rename(l.Database(), elsewhere); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, l.Database()); err != nil {
				t.Fatal(err)
			}
		},
		"query pack replaced by a link": func(t *testing.T, l Layout) {
			if err := os.RemoveAll(l.Pack()); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), l.Pack()); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ws := initialized(t)
			_ = ws.Close()
			damage(t, ws.Layout)
			if again, err := Open(ws.Layout.Root); err == nil {
				_ = again.Close()
				t.Fatal("Open accepted the damaged workspace")
			}
		})
	}
}

// Requests and sessions record who filed them; a Demo has no GitHub identity, so it names a
// fictional practitioner rather than leaving every practice control without one.
func TestInitRecordsAFictionalPractitionerWhoAdministersTheDemoAccounts(t *testing.T) {
	ws := initialized(t)
	v, err := ws.Store.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if v.Login != Practitioner || !strings.HasSuffix(v.Host, ".invalid") {
		t.Fatalf("identity %q on %q", v.Login, v.Host)
	}
	owners := map[string]bool{}
	for _, a := range v.Accounts {
		owners[a.Owner] = a.Role == "admin"
	}
	for _, r := range ws.Manifest.Repositories {
		owner, _, _ := strings.Cut(r.Name, "/")
		if !owners[owner] {
			t.Errorf("the practitioner does not administer %s", owner)
		}
	}
}

// Locks resolved while provisioning are what let a Demo run and test queries without a registry;
// each new workspace takes them unchanged, and a source that is not exactly two lock files is refused.
func TestInitTakesTheProvisionedPackLocks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	locks := t.TempDir()
	body := "---\nlockVersion: 1.0.0\ndependencies:\n  codeql/actions-all:\n    version: 0.6.2\ncompiled: false\n"
	for _, rel := range []string{"codeql-pack.lock.yml", filepath.Join("test", "codeql-pack.lock.yml")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(locks, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locks, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "demo")
	if err := Init(t.Context(), InitOptions{Root: root, Sources: corpusSources, PackLocks: locks}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pack := Layout{Root: root}.Pack()
	for _, rel := range []string{"codeql-pack.lock.yml", filepath.Join("test", "codeql-pack.lock.yml")} {
		if got, err := os.ReadFile(filepath.Join(pack, rel)); err != nil || string(got) != body {
			t.Errorf("%s: %q, %v", rel, got, err)
		}
	}

	if err := os.Remove(filepath.Join(locks, "test", "codeql-pack.lock.yml")); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context(), InitOptions{Root: filepath.Join(t.TempDir(), "demo"), Sources: corpusSources,
		PackLocks: locks}); err == nil {
		t.Fatal("Init accepted locks for only one of the two packs")
	}
}
