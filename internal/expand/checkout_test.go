package expand

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
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

// A repository holding neither workflows nor an action manifest has a permanent answer, and the
// commit it was read at: for an owned repository that is a reading, not a failure.
func TestFetchReportsNothingToAnalyze(t *testing.T) {
	origin := originRepo(t, map[string]string{"src/main.js": "console.log(1)\n"})

	c := newCheckout(t, origin)
	_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", filepath.Base(origin), "")
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want an unavailable error", err)
	}
	var nothing *NothingToAnalyze
	if !errors.As(err, &nothing) {
		t.Fatalf("err = %v, want it to say there is nothing to analyze", err)
	}
	if want := headOf(t, origin); nothing.Tree.SHA != want || nothing.Tree.Ref != frontier.HeadRef {
		t.Errorf("read at %q of %q, want %s of %s", nothing.Tree.SHA, nothing.Tree.Ref, want, frontier.HeadRef)
	}
}

func headOf(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// An unborn default branch is a fact about the repository; retried, codeql-clone spent ten attempts.
func TestFetchReportsARepositoryWithNoCommitAsUnavailable(t *testing.T) {
	requireGit(t)
	origin := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatalf("creating %s: %v", origin, err)
	}
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	c := newCheckout(t, origin)
	_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", filepath.Base(origin), "")
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want an unavailable error", err)
	}
	if !strings.Contains(err.Error(), "no commit") {
		t.Errorf("reason = %q, want it to say the default branch has no commit", err)
	}
	// Nothing to analyze, and no commit to say it of.
	var nothing *NothingToAnalyze
	if !errors.As(err, &nothing) || nothing.Tree.SHA != "" {
		t.Errorf("err = %v, want nothing to analyze with no commit", err)
	}
}

// An owned repository with no commit has nothing to analyze, which is a reading rather than a
// failure: settled, with its reason, and no attempt charged.
func TestTheCrawlSettlesAnOwnedRepositoryWithNoCommitAsNothingToAnalyze(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	origin := filepath.Join(base, "octo", "unborn")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatalf("creating %s: %v", origin, err)
	}
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	h := newHarness(t)
	h.expander.Fetcher = &Checkout{BaseURL: base}
	h.enqueue(item("octo", "unborn", "", 0))

	stats := h.run(10)

	status, sha, attempts := statusOf(t, h.store, "octo/unborn")
	if status != frontier.StatusNothing || attempts != 0 || sha != "" {
		t.Errorf("status = %q at %q after %d attempts, want nothing to analyze, no commit and no attempt",
			status, sha, attempts)
	}
	var reason string
	if err := h.store.DB().QueryRow(
		`SELECT COALESCE(reason, '') FROM abom_frontier WHERE node_key = 'octo/unborn'`).
		Scan(&reason); err != nil {
		t.Fatalf("reading reason: %v", err)
	}
	if !strings.Contains(reason, "no commit on its default branch") {
		t.Errorf("reason = %q, want the repository fact", reason)
	}
	if stats.Nothing != 1 || stats.Unavailable != 0 || stats.Failed != 0 {
		t.Errorf("nothing = %d, unavailable = %d, failed = %d, want 1, 0 and 0",
			stats.Nothing, stats.Unavailable, stats.Failed)
	}
}

// An owned repository with no workflow and no action manifest was read in full. It is recorded at
// the commit it was read at, so a push that adds a workflow is a change from nothing; and no run
// is recorded, because CodeQL refuses to build a database from no Actions source.
func TestAnOwnedRepositoryWithNoWorkflowsIsReadAtItsCommit(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	origin := filepath.Join(base, "octo", "docs")
	built := originRepo(t, map[string]string{"README.md": "docs\n"})
	if err := os.MkdirAll(filepath.Dir(origin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(built, origin); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.expander.Fetcher = &Checkout{BaseURL: base}
	h.enqueue(item("octo", "docs", "", 0))

	h.run(10)

	status, sha, attempts := statusOf(t, h.store, "octo/docs")
	if status != frontier.StatusNothing || sha != headOf(t, origin) || attempts != 0 {
		t.Errorf("status = %q at %q after %d attempts, want nothing to analyze at %s",
			status, sha, attempts, headOf(t, origin))
	}
	var analyzedAt, checkedAt sql.NullString
	var count, runs int
	if err := h.store.DB().QueryRow(`SELECT analyzed_at, checked_at, analysis_count FROM abom_frontier
		WHERE node_key = 'octo/docs'`).Scan(&analyzedAt, &checkedAt, &count); err != nil {
		t.Fatal(err)
	}
	if analyzedAt.Valid || checkedAt.Valid || count != 0 {
		t.Errorf("analyzed_at %v, checked_at %v, analysis_count %d: a reading must not stand in for an analysis or a probe",
			analyzedAt, checkedAt, count)
	}
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_run`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("%d runs recorded for a repository nothing was run against", runs)
	}
}

// What a workflow names is a dependency, and one with nothing to run cannot be run: for it,
// unavailable is the right answer still.
func TestADependencyWithNoWorkflowsIsStillUnavailable(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	origin := filepath.Join(base, "octo", "lib")
	built := originRepo(t, map[string]string{"src/main.js": "console.log(1)\n"})
	if err := os.MkdirAll(filepath.Dir(origin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(built, origin); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.expander.Fetcher = &Checkout{BaseURL: base}
	h.enqueue(item("octo", "lib", "", 1))

	stats := h.run(10)

	if status, _, _ := statusOf(t, h.store, "octo/lib"); status != frontier.StatusUnavailable {
		t.Errorf("status = %q, want unavailable", status)
	}
	if stats.Unavailable != 1 || stats.Nothing != 0 {
		t.Errorf("unavailable = %d, nothing = %d, want 1 and 0", stats.Unavailable, stats.Nothing)
	}
}

// One checkout answers both the owned repository and a workflow somebody calls in it. The
// repository has nothing to analyze; the workflow it was asked for does not exist there.
func TestOneCheckoutWithNothingSettlesTheRepositoryAndFailsTheDependency(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	origin := filepath.Join(base, "octo", "docs")
	built := originRepo(t, map[string]string{"README.md": "docs\n"})
	if err := os.MkdirAll(filepath.Dir(origin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(built, origin); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.expander.Fetcher = &Checkout{BaseURL: base}
	h.enqueue(item("octo", "docs", "", 0), item("octo", "docs", ".github/workflows/ci.yml", 1))

	stats := h.run(10)

	if status, _, _ := statusOf(t, h.store, "octo/docs"); status != frontier.StatusNothing {
		t.Errorf("repository status = %q, want nothing to analyze", status)
	}
	if status, _, _ := statusOf(t, h.store, "octo/docs/.github/workflows/ci.yml"); status != frontier.StatusUnavailable {
		t.Errorf("dependency status = %q, want unavailable", status)
	}
	if stats.Nothing != 1 {
		t.Errorf("nothing = %d, want the one checkout", stats.Nothing)
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

// Discovery runs with nobody watching. An askpass program inherited from the launching terminal,
// which VS Code sets, would leave a private repository's probe waiting on a dialog, not failing.
func TestUnattendedGitNeverUsesAnInheritedAskpassProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git stand-in is a shell script")
	}
	dir := t.TempDir()
	seen := filepath.Join(dir, "askpass-seen")
	stub := filepath.Join(dir, "git")
	script := "#!/bin/sh\n" +
		"printf '%s' \"${GIT_ASKPASS-unset}\" > \"$ABOM_TEST_ASKPASS_SEEN\"\n" +
		"printf '0123456789abcdef0123456789abcdef01234567\\tHEAD\\n'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the git stand-in: %v", err)
	}
	t.Setenv("ABOM_TEST_ASKPASS_SEEN", seen)
	t.Setenv("GIT_ASKPASS", "/Applications/Editor.app/askpass.sh")

	if _, err := (&Checkout{Git: stub}).Resolve(context.Background(), "octo", "private", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the git stand-in recorded nothing: %v", err)
	}
	if string(got) != "" {
		t.Errorf("git ran with GIT_ASKPASS=%q; an unattended probe must have no askpass program", got)
	}
}
