package lockverify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGh writes an executable standing in for `gh`, which prints body and exits with code. It
// records the arguments it was called with and the directory it ran in, so a test can see both.
// before runs first, for a fake that misbehaves.
func fakeGh(t *testing.T, body string, code int, before string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"echo \"$PWD $*\" >> '" + calls + "'\n" +
		before + "\n" +
		"cat <<'JSON'\n" + body + "\nJSON\n" +
		"exit " + string(rune('0'+code)) + "\n"
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake gh: %v", err)
	}
	return path, calls
}

// aTree is a checkout holding one workflow and its lock.
func aTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		".github/workflows/ci.yml":       "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n",
		".github/workflows/actions.lock": "version: 'v0.0.2'\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, gh, tree, mode string) Result {
	t.Helper()
	return Runner{Gh: gh, Timeout: time.Minute}.Verify(context.Background(), tree, mode)
}

// Each state the tool reports is its own outcome. A passing check with a warning is not a clean
// one, and a check that could not decide is not one that passed.
func TestEachReportedStateIsItsOwnOutcome(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		code int
		want string
	}{
		"nothing to report": {`{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[]}`,
			0, OutcomePassed},
		"a moved ref": {`{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[
			{"workflow":".github/workflows/ci.yml","category":"ref-moved","severity":"warning",
			 "dependency":"actions/checkout@v4","detail":"ref v4 now resolves to 11d5960a3267"}]}`,
			0, OutcomeWarnings},
		"not pinned": {`{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":false,"findings":[
			{"workflow":".github/workflows/ci.yml","category":"not-pinned","severity":"error",
			 "dependency":"actions/checkout@v4","detail":"no lockfile entry"}]}`,
			1, OutcomeFailed},
		"could not tell": {`{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[
			{"workflow":".github/workflows/ci.yml","category":"reachability-unknown","severity":"warning",
			 "detail":"could not confirm the pin is reachable"}]}`,
			0, OutcomeInconclusive},
		"a category this build does not know": {`{"cli_version":"v0.2.0","lockfile_version":"v0.0.2","valid":true,"findings":[
			{"workflow":".github/workflows/ci.yml","category":"something-new","severity":"info","detail":"?"}]}`,
			0, OutcomeInconclusive},
	} {
		gh, _ := fakeGh(t, c.body, c.code, "")
		got := run(t, gh, aTree(t), ModeNetwork)
		if got.Outcome != c.want {
			t.Errorf("%s: outcome %q (%s), want %q", name, got.Outcome, got.Error, c.want)
		}
		if got.Findings == nil {
			t.Errorf("%s: findings is nil", name)
		}
	}
}

// A category this build does not know is kept as the tool said it and marked, never dropped: it
// is the part of the answer nobody here can interpret yet.
func TestAnUnknownCategoryIsKeptAndMarked(t *testing.T) {
	gh, _ := fakeGh(t, `{"cli_version":"v0.2.0","lockfile_version":"v0.0.2","valid":true,"findings":[
		{"workflow":".github/workflows/ci.yml","category":"something-new","severity":"info","detail":"?"}]}`, 0, "")
	got := run(t, gh, aTree(t), ModeLocal)
	if len(got.Findings) != 1 || got.Findings[0].Category != "something-new" || got.Findings[0].Known {
		t.Errorf("findings = %+v, want the unknown category kept and marked unknown", got.Findings)
	}
	if got.ToolVersion != "v0.2.0" || got.LockfileVersion != "v0.0.2" {
		t.Errorf("versions = %q, %q; want what the tool reported", got.ToolVersion, got.LockfileVersion)
	}
}

// A tool that fails says nothing about the lock. Its message is what is recorded, and the outcome
// is an error rather than anything a reader could take for a verdict.
func TestAToolFailureIsAnErrorWithItsMessage(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\necho 'lockfile is unreadable: unsupported dependency lockfile version' >&2\nexit 2\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got := run(t, gh, aTree(t), ModeLocal)
	if got.Outcome != OutcomeError || !strings.Contains(got.Error, "unsupported dependency lockfile version") {
		t.Errorf("outcome %q, error %q; want an error carrying the tool's message", got.Outcome, got.Error)
	}
	if got.Valid != nil {
		t.Errorf("valid = %v, want nothing: the tool never said", *got.Valid)
	}

	missing := run(t, filepath.Join(dir, "no-such-gh"), aTree(t), ModeLocal)
	if missing.Outcome != OutcomeError || missing.Error == "" {
		t.Errorf("a missing tool gave %q (%q), want an error that says so", missing.Outcome, missing.Error)
	}
}

// The tool is never run in the tree it was handed, and never believed if it changed what it read.
// No-fix is meant to write nothing; a copy that changed anyway is a tool that did not honor it.
func TestTheToolRunsOnACopyAndIsNotBelievedIfItWritesToIt(t *testing.T) {
	tree := aTree(t)
	lock := filepath.Join(tree, ".github", "workflows", "actions.lock")
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	gh, calls := fakeGh(t, `{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[]}`, 0,
		"echo rewritten >> .github/workflows/actions.lock")

	got := run(t, gh, tree, ModeLocal)

	after, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the tool ran in the tree it was handed and changed it")
	}
	if got.Outcome != OutcomeError || !strings.Contains(got.Error, "rewrote") {
		t.Errorf("outcome %q (%q), want an error saying the tool rewrote its inputs", got.Outcome, got.Error)
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorded), tree+" ") {
		t.Errorf("the tool was called in the original tree: %s", recorded)
	}
}

// Always the no-fix spelling, and the mode asked for: fix mode rewrites workflows and the lock,
// and neither is ABOM's to change.
func TestTheToolIsAlwaysCalledNoFix(t *testing.T) {
	for mode, flag := range map[string]string{ModeLocal: "--verify-local", ModeNetwork: "--verify"} {
		gh, calls := fakeGh(t, `{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[]}`, 0, "")
		run(t, gh, aTree(t), mode)
		recorded, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		args := string(recorded)
		for _, want := range []string{"actions-lock " + flag + " ", "--no-fix", "--no-interactive", "--json=valid,findings"} {
			if !strings.Contains(args, want) {
				t.Errorf("%s: called with %q, want %q among the arguments", mode, args, want)
			}
		}
	}
}

// The copy is a repository of its own, because the tool reads `$/` actions through git: run in a
// bare directory it reports every `$/` step as unreadable, which is a finding about the copy.
func TestTheCopyIsARepositoryAtOneCommit(t *testing.T) {
	gh, _ := fakeGh(t, `{"cli_version":"v0.1.6","lockfile_version":"v0.0.2","valid":true,"findings":[]}`, 0,
		"git rev-parse HEAD > /dev/null 2>&1 || { echo not a repository >&2; exit 3; }")
	if got := run(t, gh, aTree(t), ModeLocal); got.Outcome != OutcomePassed {
		t.Errorf("outcome %q (%q), want passed: the copy has to be a repository", got.Outcome, got.Error)
	}
}

// The real tool, offline, against a lock it generated: the contract the fakes stand in for.
func TestThePinnedToolReadsALockItGenerated(t *testing.T) {
	if err := exec.Command("gh", "actions-lock", "--help").Run(); err != nil {
		t.Skip("gh actions-lock is not installed; `make pins` reports that on its own")
	}
	tree, err := filepath.Abs(filepath.Join("..", "..", "testdata", "corpus", "lockedcorp", "pinned-service"))
	if err != nil {
		t.Fatal(err)
	}
	got := Runner{Gh: "gh", Timeout: time.Minute}.Verify(context.Background(), tree, ModeLocal)
	if got.Outcome != OutcomePassed || got.Valid == nil || !*got.Valid {
		t.Errorf("outcome %q (%s), findings %+v; want the fixture's complete coverage to pass",
			got.Outcome, got.Error, got.Findings)
	}
	if got.ToolVersion == "" || got.LockfileVersion != "v0.0.2" {
		t.Errorf("versions %q, %q; want what the tool reported", got.ToolVersion, got.LockfileVersion)
	}
}
