package expand

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit states a prerequisite of `make test` rather than sniffing for one.
//
// These tests clone from a local path to exercise checkout against git itself, which is the only
// thing that proves the argument order and the error text. Skipping when git is absent would let
// the suite report success having quietly stopped testing the part most likely to break.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is not on PATH, and these tests need it: %v", err)
	}
}

// originRepo builds a real git repository on disk so checkout behavior is exercised against
// git itself rather than a mock of it. Cloning from a local path keeps the test hermetic.
func originRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "abom@example.test"},
		{"config", "user.name", "ABOM Test"},
		{"add", "-A"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func newCheckout(t *testing.T, origin string) *Checkout {
	t.Helper()
	return &Checkout{BaseURL: filepath.Dir(origin)}
}

func fetch(t *testing.T, origin string) Tree {
	t.Helper()
	c := newCheckout(t, origin)
	tree, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", filepath.Base(origin), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return tree
}

func exists(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// An actions database needs the action manifest and any workflows; nothing else in the tree
// contributes, and downloading it would dominate the cost of a large expansion.
func TestFetchTakesOnlyActionsContent(t *testing.T) {
	origin := originRepo(t, map[string]string{
		"action.yml":                "name: test\nruns:\n  using: node20\n",
		".github/workflows/ci.yml":  "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n",
		"src/main.js":               "console.log('irrelevant')\n",
		"docs/guide.md":             "# irrelevant\n",
		"vendor/big/library.min.js": "irrelevant\n",
	})

	tree := fetch(t, origin)

	for _, want := range []string{"action.yml", ".github/workflows/ci.yml"} {
		if !exists(t, tree.Dir, want) {
			t.Errorf("%s missing from checkout", want)
		}
	}
	for _, unwanted := range []string{"src/main.js", "docs/guide.md", "vendor/big/library.min.js"} {
		if exists(t, tree.Dir, unwanted) {
			t.Errorf("%s should not have been checked out", unwanted)
		}
	}
}

// The SHA is what makes a later drift check possible, so it has to be the real commit.
func TestFetchResolvesHeadSHA(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: test\n"})

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = origin
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	want := strings.TrimSpace(string(out))

	tree := fetch(t, origin)
	if tree.SHA != want {
		t.Errorf("SHA = %q, want %q", tree.SHA, want)
	}
	if tree.Ref != "HEAD" {
		t.Errorf("Ref = %q, want HEAD", tree.Ref)
	}
}

// Composite actions frequently live in subdirectories and reference their siblings by relative
// path, so taking manifests at any depth avoids a needless second pass.
func TestFetchTakesNestedActionManifests(t *testing.T) {
	origin := originRepo(t, map[string]string{
		"setup-gradle/action.yml":           "name: setup\n",
		"dependency-submission/action.yaml": "name: submit\n",
		"setup-gradle/dist/index.js":        "irrelevant\n",
	})

	tree := fetch(t, origin)

	for _, want := range []string{"setup-gradle/action.yml", "dependency-submission/action.yaml"} {
		if !exists(t, tree.Dir, want) {
			t.Errorf("%s missing from checkout", want)
		}
	}
	if exists(t, tree.Dir, "setup-gradle/dist/index.js") {
		t.Error("bundled javascript should not have been checked out")
	}
}

// A repository holding neither workflows nor an action manifest has a permanent answer.
func TestFetchReportsNothingToAnalyze(t *testing.T) {
	origin := originRepo(t, map[string]string{"src/main.js": "console.log(1)\n"})

	c := newCheckout(t, origin)
	_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", filepath.Base(origin), "")
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want an unavailable error", err)
	}
}

// A deleted, renamed or private repository is likewise permanent: retrying costs quota and
// cannot succeed.
func TestFetchReportsMissingRepository(t *testing.T) {
	requireGit(t)
	c := &Checkout{BaseURL: t.TempDir()}

	_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", "does-not-exist", "")
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want an unavailable error", err)
	}
}

func TestFetchIsRepeatableIntoTheSameWorkDir(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: test\n"})
	c := newCheckout(t, origin)
	dest := filepath.Join(t.TempDir(), "work")

	first, err := c.Fetch(context.Background(), dest, "", filepath.Base(origin), "")
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	second, err := c.Fetch(context.Background(), dest, "", filepath.Base(origin), "")
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if first.SHA != second.SHA {
		t.Errorf("SHA changed across fetches: %q then %q", first.SHA, second.SHA)
	}
}

func TestCloneURLUsesOwnerAndRepo(t *testing.T) {
	c := &Checkout{}
	if got := c.cloneURL("actions", "checkout"); got != "https://github.com/actions/checkout.git" {
		t.Errorf("cloneURL = %q", got)
	}
}
