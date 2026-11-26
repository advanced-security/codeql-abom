package expand

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// gitSaying stands in for git: every command prints said on stderr and exits 128, as Git does for a
// resolver outage and for a deleted repository alike.
func gitSaying(t *testing.T, said string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the git stand-in is a shell script")
	}
	stub := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\ncat >&2 <<'SAID'\n" + said + "\nSAID\nexit 128\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

const (
	resolverOutage = "fatal: unable to access 'https://github.com/octo/lib.git/': Could not resolve host: github.com"
	missingBranch  = "warning: Could not find remote branch v9 to clone.\nfatal: Remote branch v9 not found in upstream origin"
	refused        = "fatal: unable to access 'https://github.com/octo/lib.git/': The requested URL returned error: 403"
)

// One resolver outage used to settle a repository unavailable for good. Only an answer a later read
// would repeat may end coverage; an outage is retried.
func TestAFetchThatFailsOnlyForNowIsRetriedRatherThanUnavailable(t *testing.T) {
	for said, terminal := range map[string]bool{resolverOutage: false, missingBranch: true, refused: true} {
		c := &Checkout{Git: gitSaying(t, said)}
		_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"), "octo", "lib", "v9")
		if err == nil || IsUnavailable(err) != terminal {
			t.Errorf("Fetch with git saying %q = %v; unavailable %v, want %v", said, err, IsUnavailable(err), terminal)
		}
		_, err = c.Resolve(context.Background(), "octo", "lib", "v9")
		if err == nil || IsUnavailable(err) != terminal {
			t.Errorf("Resolve with git saying %q = %v; unavailable %v, want %v", said, err, IsUnavailable(err), terminal)
		}
	}
}

// Through the crawl: the outage leaves the row pending with one attempt spent and a retry
// scheduled, and the same row fetched from a missing branch is settled unavailable.
func TestTheCrawlRetriesARepositoryAnOutageStopped(t *testing.T) {
	for said, want := range map[string]string{resolverOutage: frontier.StatusPending, missingBranch: frontier.StatusUnavailable} {
		h := newHarness(t)
		h.expander.Fetcher = &Checkout{Git: gitSaying(t, said)}
		h.enqueue(item("octo", "lib", "", 1))

		h.run(10)

		status, _, attempts := statusOf(t, h.store, "octo/lib")
		if status != want {
			t.Errorf("git saying %q: status %q, want %q", said, status, want)
		}
		if want == frontier.StatusPending && attempts != 1 {
			t.Errorf("git saying %q: %d attempts, want 1 with a retry scheduled", said, attempts)
		}
	}
}

// A probe is how an analysis is kept fresh. An outage during one says nothing about the analysis,
// which stays done and is probed again next pass.
func TestAProbeThatFailsOnlyForNowLeavesTheAnalysisDone(t *testing.T) {
	h := newHarness(t)
	analyzedAtRef(t, h.queue, "octo", "lib", "v4", "1111111111111111111111111111111111111111")
	r := &Rechecker{Queue: h.queue, Probe: &Checkout{Git: gitSaying(t, resolverOutage)}}

	stats, err := r.Run(context.Background(), -time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := statusOf(t, h.store, "octo/lib"); status != frontier.StatusDone || stats.Unavailable != 0 {
		t.Errorf("after an outage during a probe: status %q, %d unavailable; want done and none", status, stats.Unavailable)
	}
}

// The working database's own unavailable reasons, and the outage the classification exists for. A
// clone, fetch or probe that failed only for now goes back; anything else stays, whatever its words,
// because only a clone's reason carries Git's.
func TestARowSettledForAReasonNoLongerTerminalGoesBack(t *testing.T) {
	const notInstalled = "; the GitHub App is not installed on this account: actions, so a private repository there cannot be read"
	for _, c := range []struct {
		reason string
		depth  int
		back   bool
	}{
		{"dependency unavailable: cloning https://github.com/octo/lib.git at \"v1\": " + resolverOutage, 1, true},
		{"dependency unavailable: resolving octo/lib@v1: fatal: early EOF", 2, true},
		{"dependency unavailable: fetching 0123 of lib: error: RPC failed", 1, true},
		{"dependency unavailable: cloning https://github.com/actions/publish-immutable-action.git at \"0.0.3\": " +
			"Cloning into '.'...\nfatal: Remote branch 0.0.3 not found in upstream origin" + notInstalled, 2, false},
		{"dependency unavailable: cloning https://github.com/octo/private.git at \"\": remote: Repository not found.", 1, false},
		{"dependency unavailable: codeql-mrva-dev has no workflows or action manifest", 0, false},
		{"dependency unavailable: codeql-clone has no commit on its default branch: fatal: You are on a branch yet to be born", 0, false},
		{"dependency unavailable: lib has no workflows or action manifest", 1, false},
		{"source policy: octo/private: GitHub reports Code Security/Advanced Security disabled; enable it and retry", 0, false},
		{"no source code seen during extraction", 1, false},
	} {
		row := store.UnavailableRow{NodeKey: "x/y", Reason: c.reason, Depth: c.depth}
		if got := Reconsidered(row); got != c.back {
			t.Errorf("Reconsidered(%q, depth %d) = %v, want %v", c.reason, c.depth, got, c.back)
		}
	}
}

// A dependency is named by no event, so only a probe of its reference notices that it came back.
// One that answers goes back as a resync would; one still refused keeps its reason and is not
// probed again for a day; one that cannot be answered now stays as it was and stays due. An answer
// about what a commit holds is never probed, because only reading the commit again could change it.
func TestAnUnavailableReferenceThatAnswersAgainGoesBack(t *testing.T) {
	r, q, p, s := newRechecker(t)
	refused := func(name string) string {
		return ErrUnavailable.Error() + ": cloning https://github.com/octo/" + name + ".git at \"v1\": remote: Repository not found."
	}
	for name, reason := range map[string]string{
		"back": refused("back"), "gone": refused("gone"), "flaky": refused("flaky"),
		"empty": ErrUnavailable.Error() + ": empty has no workflows or action manifest",
	} {
		k := frontier.Key{NodeKey: "octo/" + name, Ref: "v1"}
		if _, err := q.Enqueue([]frontier.Item{{NodeKey: k.NodeKey, Owner: "octo", Repo: name, Ref: "v1", Depth: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := q.Unavailable(k, reason); err != nil {
			t.Fatal(err)
		}
	}
	p.answers["octo/back@v1"] = "0123"
	p.errs["octo/gone@v1"] = fmt.Errorf("%w: resolving octo/gone@v1: remote: Repository not found.", ErrUnavailable)
	p.errs["octo/flaky@v1"] = fmt.Errorf("resolving octo/flaky@v1: %s", resolverOutage)
	stale(q, 48*time.Hour)

	stats, err := r.Reprobe(context.Background(), 24*time.Hour, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(p.asked)
	if want := []string{"octo/back@v1", "octo/flaky@v1", "octo/gone@v1"}; !slices.Equal(p.asked, want) {
		t.Errorf("probed %v, want %v", p.asked, want)
	}
	if stats.Checked != 3 || stats.Back != 1 || stats.Unavailable != 1 || stats.Failed != 1 {
		t.Errorf("stats = %+v, want 3 checked: 1 back, 1 still unavailable, 1 unanswered", stats)
	}
	for name, want := range map[string]string{"back": "pending", "gone": "unavailable", "flaky": "unavailable", "empty": "unavailable"} {
		if got := frontierStatus(t, s, "octo/"+name); got != want {
			t.Errorf("octo/%s = %q, want %q", name, got, want)
		}
	}
	var reason string
	if err := s.DB().QueryRow(`SELECT COALESCE(reason, '') FROM abom_frontier WHERE node_key = 'octo/gone'`).Scan(&reason); err != nil || reason != refused("gone") {
		t.Errorf("octo/gone's reason became %q, %v; want what the clone said kept", reason, err)
	}
	due, err := q.DueForReprobe(24*time.Hour, nil, 10, probeable)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].NodeKey != "octo/flaky" {
		t.Errorf("due after the pass = %+v, want octo/flaky alone", due)
	}
}

// Only a fix of access heals an access failure, and nothing heals a reference that is gone, so a
// reader is told which one an unavailable row is. A reason whose words came from somewhere other
// than Git or a probe is left unnamed rather than guessed at: its own text says what it is.
func TestAnUnavailableRowNamesItsCauseOnlyFromWhatGitOrAProbeSaid(t *testing.T) {
	for reason, want := range map[string]string{
		"dependency unavailable: cloning https://github.com/octo/private.git at \"\": remote: Repository not found.": CauseAccess,
		"dependency unavailable: resolving octo/lib@v1: remote: Invalid username or password.":                       CauseAccess,
		"dependency unavailable: cloning https://github.com/actions/publish-immutable-action.git at \"0.0.3\": " +
			"Cloning into '.'...\nfatal: Remote branch 0.0.3 not found in upstream origin": CauseAbsent,
		"dependency unavailable: reference missing: octo/lib has no v9":                        CauseAbsent,
		"dependency unavailable: lib has no workflows or action manifest":                      "",
		"source policy: octo/private: GitHub reports Code Security/Advanced Security disabled": "",
		"no source code seen during extraction":                                                "",
		"":                                                                                     "",
	} {
		if got := Cause(reason); got != want {
			t.Errorf("Cause(%q) = %q, want %q", reason, got, want)
		}
	}
}

// gitFailingAt stands in for git: the subcommand sub prints said on stderr and exits 128, and every
// other command succeeds saying nothing.
func gitFailingAt(t *testing.T, sub, said string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = " + sub + " ]; then\n    cat >&2 <<'SAID'\n" + said +
		"\nSAID\n    exit 128\n  fi\ndone\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

// A failure's reason is written by one function and read back by three others, by its wording. Each
// reason here comes from the code that stores it, so rewording one fails here rather than quietly
// reclassifying the rows it settled.
func TestEveryReasonAGitFailureStoresIsReadBackAsWhatItSaid(t *testing.T) {
	commit := strings.Repeat("a", 40)
	fetch := func(sub, said, ref string) error {
		c := &Checkout{Git: gitFailingAt(t, sub, said)}
		_, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"), "octo", "lib", ref)
		return err
	}
	resolve := func(sub, said string) error {
		_, err := (&Checkout{Git: gitFailingAt(t, sub, said)}).Resolve(context.Background(), "octo", "lib", "v9")
		return err
	}
	for _, c := range []struct {
		name          string
		err           error
		probeable     bool
		cause         string
		reconsidering bool
	}{
		{"a refused clone", fetch("clone", refused, "v9"), true, CauseAccess, false},
		{"a missing branch", fetch("clone", missingBranch, "v9"), true, CauseAbsent, false},
		{"a refused fetch of a commit", fetch("fetch", refused, commit), false, CauseAccess, false},
		{"a refused probe", resolve("ls-remote", refused), true, CauseAccess, false},
		{"a probe listing no such reference", resolve("never", ""), true, CauseAbsent, false},
	} {
		if !IsUnavailable(c.err) {
			t.Fatalf("%s: %v is not unavailable", c.name, c.err)
		}
		reason := c.err.Error()
		if got := probeable(reason); got != c.probeable {
			t.Errorf("%s: probeable %v, want %v: %q", c.name, got, c.probeable, reason)
		}
		if got := Cause(reason); got != c.cause {
			t.Errorf("%s: cause %q, want %q: %q", c.name, got, c.cause, reason)
		}
		if got := Reconsidered(store.UnavailableRow{Reason: reason, Depth: 1}); got != c.reconsidering {
			t.Errorf("%s: reconsidered %v, want %v: %q", c.name, got, c.reconsidering, reason)
		}
	}
}
