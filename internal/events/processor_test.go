package events

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	shaA   = "a000000000000000000000000000000000000001"
	shaB   = "b000000000000000000000000000000000000002"
	shaC   = "c000000000000000000000000000000000000003"
	forged = "f000000000000000000000000000000000000666"
)

// fixture keeps the store on disk so a test can restart it; both fakes log calls in one sequence.
type fixture struct {
	t          *testing.T
	path       string
	store      *store.Store
	queue      *frontier.Queue
	calls      []string
	answers    map[string]string
	probeErrs  map[string]error
	branches   map[string][]string
	branchErrs map[string]error
}

func (f *fixture) Resolve(_ context.Context, owner, repo, ref string) (string, error) {
	key := owner + "/" + repo + "@" + ref
	f.calls = append(f.calls, "probe "+key)
	if err := f.probeErrs[key]; err != nil {
		return "", err
	}
	return f.answers[key], nil
}

func (f *fixture) readBranches(_ context.Context, repository string) ([]string, error) {
	f.calls = append(f.calls, "branches "+repository)
	if err := f.branchErrs[repository]; err != nil {
		return nil, err
	}
	return f.branches[repository], nil
}

func (f *fixture) open() {
	f.t.Helper()
	s, err := store.Open(f.path, store.Options{})
	if err != nil {
		f.t.Fatalf("store.Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		f.t.Fatalf("Migrate: %v", err)
	}
	f.store = s
	f.queue = &frontier.Queue{Store: s}
}

// reopen is a restart: nothing held in memory survives it.
func (f *fixture) reopen() {
	f.t.Helper()
	if err := f.store.Close(); err != nil {
		f.t.Fatalf("Close: %v", err)
	}
	f.open()
}

// newFixture declares acme and holds acme/service, default branch main, analyzed at shaA at depth 0.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t: t, path: filepath.Join(t.TempDir(), "abom.db"),
		answers: map[string]string{}, probeErrs: map[string]error{},
		branches: map[string][]string{}, branchErrs: map[string]error{},
	}
	f.open()
	t.Cleanup(func() { f.store.Close() })
	if err := f.store.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	f.owned("acme/service", "main", shaA)
	return f
}

// owned records an owned repository and its root row analyzed at sha.
func (f *fixture) owned(fullName, defaultBranch, sha string) {
	f.t.Helper()
	if _, err := f.store.InsertOwnedRepository(fullName); err != nil {
		f.t.Fatalf("InsertOwnedRepository: %v", err)
	}
	if err := f.store.SetDefaultBranch(fullName, defaultBranch); err != nil {
		f.t.Fatalf("SetDefaultBranch: %v", err)
	}
	f.analyzed(fullName, "", 0, sha)
}

func (f *fixture) analyzed(fullName, ref string, depth int, sha string) {
	f.t.Helper()
	owner, repo, _ := strings.Cut(fullName, "/")
	if _, err := f.queue.Enqueue([]frontier.Item{
		{NodeKey: fullName, Ref: ref, Owner: owner, Repo: repo, Depth: depth},
	}); err != nil {
		f.t.Fatalf("Enqueue: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: fullName, Ref: ref},
		frontier.Resolution{Ref: ref, SHA: sha}); err != nil {
		f.t.Fatalf("Complete: %v", err)
	}
}

func (f *fixture) processor() *Processor {
	return &Processor{Store: f.store, Queue: f.queue, Probe: f, Branches: f.readBranches}
}

func (f *fixture) drain() Pass {
	f.t.Helper()
	pass, err := f.processor().Drain(context.Background())
	if err != nil {
		f.t.Fatalf("Drain: %v", err)
	}
	return pass
}

func (f *fixture) deliver(id, event string, payload []byte) {
	f.t.Helper()
	if _, err := f.store.AppendDelivery(store.EventDelivery{
		DeliveryID: id, Event: event, Payload: payload,
	}); err != nil {
		f.t.Fatalf("AppendDelivery: %v", err)
	}
}

func (f *fixture) delivery(id string) store.EventDelivery {
	f.t.Helper()
	d, found, err := f.store.Delivery(id)
	if err != nil || !found {
		f.t.Fatalf("Delivery(%s) = found %v, %v", id, found, err)
	}
	return d
}

func (f *fixture) row(fullName, ref string) store.QueueRow {
	f.t.Helper()
	r, found, err := f.store.QueueRow(fullName, ref)
	if err != nil || !found {
		f.t.Fatalf("QueueRow(%s@%s) = found %v, %v", fullName, ref, found, err)
	}
	return r
}

func (f *fixture) settled(id, status, reason string) store.EventDelivery {
	f.t.Helper()
	d := f.delivery(id)
	if d.Status != status || d.Reason != reason {
		f.t.Errorf("%s settled as %s (%s: %s), want %s (%s)",
			id, d.Status, d.Reason, d.Detail, status, reason)
	}
	return d
}

// The probe decides; a moved branch goes ahead of the ranking, and the graph keeps what it read.
func TestAPushToAnOwnedDefaultBranchIsProbedAndRequeuedAheadOfTheRanking(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/popular", "main", shaA)
	if _, err := f.store.DB().Exec(`UPDATE abom_frontier SET status = 'pending', score = 1000
		WHERE node_key = 'acme/popular'`); err != nil {
		t.Fatalf("ranking a competitor first: %v", err)
	}
	f.answers["acme/service@"] = shaB

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.drain()

	f.settled("d-1", store.EventDone, ReasonRequeued)
	if !slices.Equal(f.calls, []string{"probe acme/service@"}) {
		t.Errorf("calls = %v, want one probe of the default branch's row", f.calls)
	}
	row := f.row("acme/service", "")
	if row.Status != frontier.StatusPending || !row.Expedited {
		t.Errorf("row = %+v, want pending and expedited", row)
	}
	if row.ResolvedSHA != shaA {
		t.Errorf("resolved_sha = %s, want %s until the re-analysis replaces it", row.ResolvedSHA, shaA)
	}
	claimed, err := f.queue.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].NodeKey != "acme/service" {
		t.Errorf("claimed %+v first, want acme/service ahead of the better-ranked row", claimed)
	}
}

// A replayed or forged payload SHA must never move what the corpus believes it read; the probe may.
func TestAForgedPayloadSHADoesNotMoveTheRecordedCommitUnlessTheProbeAgrees(t *testing.T) {
	t.Run("the probe finds the branch where it was", func(t *testing.T) {
		f := newFixture(t)
		f.answers["acme/service@"] = shaA

		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, forged))
		f.drain()

		d := f.settled("d-1", store.EventDone, ReasonUnchanged)
		row := f.row("acme/service", "")
		if row.Status != frontier.StatusDone || row.ResolvedSHA != shaA || row.Expedited {
			t.Errorf("row = %+v, want it left done at %s and not expedited", row, shaA)
		}
		if row.CheckedAt == "" {
			t.Error("the probe was not recorded as a probe")
		}
		if !strings.Contains(d.Detail, forged) {
			t.Errorf("detail %q does not say what the payload claimed", d.Detail)
		}
	})

	t.Run("the probe finds it somewhere else", func(t *testing.T) {
		f := newFixture(t)
		f.answers["acme/service@"] = shaC

		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, forged))
		f.drain()

		f.settled("d-1", store.EventDone, ReasonRequeued)
		row := f.row("acme/service", "")
		if !strings.Contains(row.Reason, shaC) || strings.Contains(row.Reason, forged) {
			t.Errorf("queue reason %q, want the probe's %s and never the payload's", row.Reason, shaC)
		}
		if row.ResolvedSHA != shaA {
			t.Errorf("resolved_sha = %s, want %s", row.ResolvedSHA, shaA)
		}
	})

	t.Run("nothing anywhere records the forged commit", func(t *testing.T) {
		f := newFixture(t)
		f.answers["acme/service@"] = shaA
		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, forged))
		f.drain()
		var n int
		if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier
			WHERE resolved_sha = ? OR COALESCE(reason, '') LIKE ?`, forged, "%"+forged+"%").
			Scan(&n); err != nil {
			t.Fatalf("counting: %v", err)
		}
		if n != 0 {
			t.Errorf("%d queue rows carry the forged commit", n)
		}
	})
}

// A protected branch is live for its repository, at the depth-0 row protection queued for it.
func TestAPushToAProtectedBranchIsProbedAtThatBranch(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.SetProtectedBranches("acme/service", []string{"release"}); err != nil {
		t.Fatalf("SetProtectedBranches: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service", Ref: "release"},
		frontier.Resolution{Ref: "release", SHA: shaA}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f.answers["acme/service@release"] = shaB

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/release", shaA, shaB))
	f.drain()

	f.settled("d-1", store.EventDone, ReasonRequeued)
	if !slices.Equal(f.calls, []string{"probe acme/service@release"}) {
		t.Errorf("calls = %v, want the release row probed at release", f.calls)
	}
	if row := f.row("acme/service", "release"); row.Status != frontier.StatusPending || !row.Expedited {
		t.Errorf("release row = %+v, want pending and expedited", row)
	}
	if row := f.row("acme/service", ""); row.Status != frontier.StatusDone || row.Expedited {
		t.Errorf("default row = %+v, want it untouched", row)
	}
}

// A queued row is only moved forward: a push does not wipe the queue's failure history.
func TestAPushToABranchAlreadyQueuedOnlyMovesItForward(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.DB().Exec(`UPDATE abom_frontier
		SET status = 'pending', attempts = 2, reason = 'exit status 32'
		WHERE node_key = 'acme/service' AND ref = ''`); err != nil {
		t.Fatalf("failing the row: %v", err)
	}
	f.answers["acme/service@"] = shaB

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.drain()

	f.settled("d-1", store.EventDone, ReasonExpedited)
	row := f.row("acme/service", "")
	if !row.Expedited || row.Reason != "exit status 32" {
		t.Errorf("row = %+v, want expedited with its own reason kept", row)
	}
	var attempts int
	_ = f.store.DB().QueryRow(`SELECT attempts FROM abom_frontier
		WHERE node_key = 'acme/service' AND ref = ''`).Scan(&attempts)
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

// Owned only, and only where a queue row stands at the branch. Anything else is recorded with why,
// and costs no probe.
func TestPushesThatAreNotOursOrNotLiveAreIgnoredWithAReason(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		reason  string
	}{
		{"a repository nobody declared",
			pushPayload("other/thing", "refs/heads/main", shaA, shaB), ReasonNotOwned},
		{"a declared repository enumeration has not listed",
			pushPayload("acme/brand-new", "refs/heads/main", shaA, shaB), ReasonNotEnumerated},
		{"a branch that is neither default nor protected",
			pushPayload("acme/service", "refs/heads/feature", shaA, shaB), ReasonNotLive},
		{"a tag nothing stands at",
			pushPayload("acme/service", "refs/tags/v1", zeroSHA, shaB), ReasonNotReferenced},
		{"a note",
			pushPayload("acme/service", "refs/notes/commits", shaA, shaB), ReasonNotBranch},
		{"a deleted branch",
			pushPayload("acme/service", "refs/heads/main", shaA, zeroSHA), ReasonRefDeleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.store.UpsertRepository("other/thing", ""); err != nil {
				t.Fatalf("UpsertRepository: %v", err)
			}
			f.analyzed("acme/service", "dev", 2, shaA)

			f.deliver("d-1", "push", tc.payload)
			f.drain()

			d := f.settled("d-1", store.EventIgnored, tc.reason)
			if d.Detail == "" {
				t.Error("ignored without saying what was ignored")
			}
			if len(f.calls) != 0 {
				t.Errorf("calls = %v, want nothing asked", f.calls)
			}
		})
	}
}

// Protection moves what is live, so a change re-reads it with enumeration's reader and rows follow.
func TestABranchProtectionChangeRefreshesTheDepthZeroRows(t *testing.T) {
	for _, tc := range []struct {
		event   string
		payload []byte
	}{
		{"branch_protection_rule", protectionRulePayload("acme/service", "created", "hotfix")},
		{"repository_ruleset", rulesetPayload("acme/service", "acme")},
		{"create", refPayload("acme/service", "hotfix", "branch")},
		{"delete", refPayload("acme/service", "release", "branch")},
	} {
		t.Run(tc.event, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.store.SetProtectedBranches("acme/service", []string{"release"}); err != nil {
				t.Fatalf("SetProtectedBranches: %v", err)
			}
			f.branches["acme/service"] = []string{"hotfix"}

			f.deliver("d-1", tc.event, tc.payload)
			f.drain()

			f.settled("d-1", store.EventDone, ReasonBranchesRefreshed)
			if !slices.Equal(f.calls, []string{"branches acme/service"}) {
				t.Errorf("calls = %v, want one branch read", f.calls)
			}
			if row := f.row("acme/service", "hotfix"); row.Depth != 0 {
				t.Errorf("hotfix row = %+v, want depth 0", row)
			}
			if _, found, _ := f.store.QueueRow("acme/service", "release"); found {
				t.Error("release lost protection and kept its row")
			}
		})
	}

	t.Run("nothing is read for what is not ours or not a branch", func(t *testing.T) {
		f := newFixture(t)
		f.deliver("d-1", "branch_protection_rule", protectionRulePayload("other/thing", "created", "x"))
		f.deliver("d-2", "create", refPayload("acme/service", "v2", "tag"))
		f.deliver("d-3", "repository_ruleset", orgRuleset("acme", "created", map[string]any{"target": "tag"}, nil))
		f.drain()

		f.settled("d-1", store.EventIgnored, ReasonNotOwned)
		f.settled("d-2", store.EventIgnored, ReasonNotReferenced)
		f.settled("d-3", store.EventIgnored, ReasonProtectsNoBranch)
		if len(f.calls) != 0 {
			t.Errorf("calls = %v, want nothing read", f.calls)
		}
	})

	t.Run("a read that fails leaves the recorded answer", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.SetProtectedBranches("acme/service", []string{"release"}); err != nil {
			t.Fatalf("SetProtectedBranches: %v", err)
		}
		f.branchErrs["acme/service"] = errors.New("HTTP 502")
		f.deliver("d-1", "branch_protection_rule", protectionRulePayload("acme/service", "deleted", "release"))
		f.drain()

		d := f.settled("d-1", store.EventFailed, ReasonBranchReadFailed)
		if !strings.Contains(d.Detail, "HTTP 502") {
			t.Errorf("detail %q does not carry the failure", d.Detail)
		}
		if _, found, _ := f.store.QueueRow("acme/service", "release"); !found {
			t.Error("a failed read removed a protected branch's row")
		}
	})
}

// A receiver that loses an event on restart is a sweep with extra steps; acting twice is no better.
func TestAnAcknowledgedDeliverySurvivesARestartAndIsProcessedExactlyOnce(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaB
	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))

	f.reopen()
	f.drain()
	f.drain()
	f.reopen()
	f.drain()

	f.settled("d-1", store.EventDone, ReasonRequeued)
	if !slices.Equal(f.calls, []string{"probe acme/service@"}) {
		t.Errorf("calls = %v, want exactly one probe across restarts", f.calls)
	}
	if d := f.delivery("d-1"); d.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", d.Attempts)
	}
}

// The log is processed in the order deliveries were committed, never by id or by payload.
func TestDeliveriesAreProcessedInTheOrderTheyArrived(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/one", "main", shaA)
	f.owned("acme/two", "main", shaA)
	f.answers["acme/one@"] = shaA
	f.answers["acme/two@"] = shaA
	f.answers["acme/service@"] = shaA

	f.deliver("zz", "push", pushPayload("acme/two", "refs/heads/main", shaA, shaB))
	f.deliver("aa", "branch_protection_rule", protectionRulePayload("acme/one", "edited", "main"))
	f.deliver("mm", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.deliver("bb", "push", pushPayload("acme/one", "refs/heads/main", shaA, shaB))
	f.drain()

	want := []string{"probe acme/two@", "branches acme/one", "probe acme/service@", "probe acme/one@"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
}

// A push to a row under analysis waits for what it read, without holding up the deliveries behind.
func TestAPushWhoseBranchIsBeingAnalyzedWaitsWithoutHoldingUpTheRest(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/other", "main", shaA)
	if _, err := f.store.DB().Exec(`UPDATE abom_frontier SET status = 'running'
		WHERE node_key = 'acme/service' AND ref = ''`); err != nil {
		t.Fatalf("claiming the row: %v", err)
	}
	f.answers["acme/other@"] = shaB

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.deliver("d-2", "push", pushPayload("acme/other", "refs/heads/main", shaA, shaB))
	pass := f.drain()

	if pass.Deferred != 1 || pass.Settled != 1 {
		t.Errorf("pass = %+v, want one deferred and one settled", pass)
	}
	waiting := f.delivery("d-1")
	if waiting.Status != store.EventPending || waiting.Reason != ReasonAnalysisRunning {
		t.Errorf("d-1 = %+v, want pending while the analysis runs", waiting)
	}
	f.settled("d-2", store.EventDone, ReasonRequeued)
	if !slices.Equal(f.calls, []string{"probe acme/other@"}) {
		t.Errorf("calls = %v, want only acme/other probed", f.calls)
	}

	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service"},
		frontier.Resolution{SHA: shaB}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f.answers["acme/service@"] = shaB
	f.drain()
	f.settled("d-1", store.EventDone, ReasonUnchanged)
}

// A probe that cannot answer is a recorded failure, never read as unchanged, and the queue is left.
func TestAProbeThatFailsLeavesTheQueueAloneAndSaysSo(t *testing.T) {
	f := newFixture(t)
	f.probeErrs["acme/service@"] = errors.New("ls-remote: could not resolve host")

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.drain()

	d := f.settled("d-1", store.EventFailed, ReasonProbeFailed)
	if !strings.Contains(d.Detail, "could not resolve host") {
		t.Errorf("detail %q does not carry the failure", d.Detail)
	}
	row := f.row("acme/service", "")
	if row.Status != frontier.StatusDone || row.Expedited || row.CheckedAt != "" {
		t.Errorf("row = %+v, want it untouched", row)
	}
}

// What this build does not act on is kept and named, so the status shows what was left to the sweep.
func TestEventsNotActedOnAreRecordedAndNamed(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		id, event string
		payload   []byte
		status    string
		reason    string
		mention   string
	}{
		{"r-1", "repository", repositoryPayload("acme/service", "archived"),
			store.EventIgnored, ReasonNotActedOn, "archived"},
		{"r-2", "repository", repositoryPayload("acme/service", "renamed"),
			store.EventIgnored, ReasonNotActedOn, "renamed"},
		{"r-3", "repository", repositoryPayload("acme/service", "transferred"),
			store.EventIgnored, ReasonNotActedOn, "transferred"},
		{"r-4", "repository", repositoryPayload("acme/service", "deleted"),
			store.EventIgnored, ReasonNotActedOn, "deleted"},
		{"i-1", "installation", installationPayload("created", "acme"),
			store.EventIgnored, ReasonNotActedOn, "created"},
		{"i-2", "installation_repositories", installationPayload("added", "acme"),
			store.EventIgnored, ReasonNotActedOn, "added"},
		{"p-1", "ping", pingPayload(), store.EventIgnored, ReasonPing, "ping"},
		{"x-1", "issues", repositoryPayload("acme/service", "opened"),
			store.EventIgnored, ReasonUnhandled, "issues"},
		{"bad", "push", []byte(`{"ref": "refs/heads/main",`),
			store.EventFailed, ReasonUnparseable, "push"},
	}
	for _, c := range cases {
		f.deliver(c.id, c.event, c.payload)
	}
	f.drain()

	for _, c := range cases {
		d := f.settled(c.id, c.status, c.reason)
		if !strings.Contains(d.Detail, c.mention) {
			t.Errorf("%s detail %q does not mention %q", c.id, d.Detail, c.mention)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v, want nothing asked", f.calls)
	}
}

// An outcome without a reason is a number nobody can check.
func TestEverySettledDeliveryCarriesAReason(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaB
	f.deliver("1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.deliver("2", "push", pushPayload("other/thing", "refs/heads/main", shaA, shaB))
	f.deliver("3", "create", refPayload("acme/service", "x", "branch"))
	f.deliver("4", "meta", encode(map[string]any{"action": "deleted", "hook_id": 1}))
	f.drain()

	var n int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_event_delivery
		WHERE status <> 'pending' AND (reason = '' OR detail = '' OR processed_at IS NULL)`).
		Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 0 {
		t.Errorf("%d settled deliveries carry no reason, detail or time", n)
	}
	var pending int
	_ = f.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_event_delivery WHERE status = 'pending'`).
		Scan(&pending)
	if pending != 0 {
		t.Errorf("%d deliveries left pending", pending)
	}
}

// An event is often the first word that a declared account holds a repository no listing has named.
// That repository is read on the next cycle; an installation changes which repositories the App
// reaches, which a listing of the account reads. has-ghas's first pushes were ignored for an hour.
func TestAnEventAboutADeclaredAccountsRepositoriesHasItReadOrListed(t *testing.T) {
	for _, tc := range []struct {
		name, event string
		payload     []byte
		reason      string
		want        string
	}{
		{"a push to a repository not listed yet", "push",
			pushPayload("acme/brand-new", "refs/heads/main", zeroSHA, shaB), ReasonReadRequested, "read acme/brand-new"},
		{"a branch created in one", "create", refPayload("acme/brand-new", "main", "branch"),
			ReasonReadRequested, "read acme/brand-new"},
		{"repositories added to the App", "installation_repositories", installationPayload("added", "acme"),
			ReasonListingRequested, "list acme"},
		{"the App installed", "installation", installationPayload("created", "acme"),
			ReasonListingRequested, "list acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			var a asked
			p := a.hook(f.processor())
			f.deliver("d-1", tc.event, tc.payload)
			drainWith(t, p)

			d := f.settled("d-1", store.EventDone, tc.reason)
			calls := slices.DeleteFunc(slices.Clone(a.calls), func(c string) bool { return c == "installations" })
			if !slices.Equal(calls, []string{tc.want}) || !strings.Contains(d.Detail, "acme") {
				t.Errorf("asked %v, saying %q; want %q once and acme named", a.calls, d.Detail, tc.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("calls = %v, want nothing probed before a listing names it", f.calls)
			}
		})
	}
}

// The App's installations are evidence beside the declaration, so an installation event re-reads
// them whichever account it is on; only a declared account is listed.
func TestInstallationEventsReReadTheInstallationsAndListOnlyDeclaredAccounts(t *testing.T) {
	f := newFixture(t)
	var listed []string
	var reread int
	p := f.processor()
	p.List = func(owner string) { listed = append(listed, owner) }
	p.Installations = func() { reread++ }
	f.deliver("i-1", "installation", installationPayload("created", "other"))
	f.deliver("i-2", "installation_repositories", installationPayload("removed", "acme"))
	f.deliver("r-1", "repository", repositoryPayload("other/thing", "created"))
	if _, err := p.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	for id, reason := range map[string]string{"i-1": ReasonNotOwned, "r-1": ReasonNotOwned} {
		if d := f.settled(id, store.EventIgnored, reason); !strings.Contains(d.Detail, "other") {
			t.Errorf("%s says %q, which does not name the undeclared account", id, d.Detail)
		}
	}
	f.settled("i-2", store.EventDone, ReasonListingRequested)
	if !slices.Equal(listed, []string{"acme"}) || reread != 2 {
		t.Errorf("listed %v and re-read installations %d times; want acme alone, and twice", listed, reread)
	}
}

// A push moves every reference to its branch. A workflow elsewhere using this repository's action at
// that branch reads it through a row of its own, and the poller leaves owned repositories to events:
// test-repo-2 kept reading a composite action that a push to test-repo-1's main had replaced.
func TestAPushRequeuesEveryRowStandingAtItsBranch(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "main", 1, shaA)
	f.analyzed("acme/service", "dev", 2, shaA)
	f.analyzed("acme/service", "v1", 1, shaA)
	if _, err := f.queue.Enqueue([]frontier.Item{{NodeKey: "acme/service/lint", Ref: "main",
		Owner: "acme", Repo: "service", Subpath: "lint", Depth: 2}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service/lint", Ref: "main"},
		frontier.Resolution{Ref: "main", SHA: shaA}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f.answers["acme/service@"] = shaB
	f.answers["acme/service@main"] = shaB
	f.answers["acme/service@dev"] = shaC

	f.deliver("main", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	f.deliver("dev", "push", pushPayload("acme/service", "refs/heads/dev", shaA, shaC))
	f.drain()

	f.settled("main", store.EventDone, ReasonRequeued)
	f.settled("dev", store.EventDone, ReasonRequeued)
	for _, k := range [][2]string{
		{"acme/service", ""}, {"acme/service", "main"}, {"acme/service/lint", "main"}, {"acme/service", "dev"},
	} {
		if r := f.row(k[0], k[1]); r.Status != frontier.StatusPending || !r.Expedited {
			t.Errorf("%s@%s is %s (expedited %v), want requeued ahead of the ranking",
				k[0], k[1], r.Status, r.Expedited)
		}
	}
	if r := f.row("acme/service", "v1"); r.Status != frontier.StatusDone {
		t.Errorf("acme/service@v1 is %s, but no push moved v1", r.Status)
	}
	want := []string{"probe acme/service@", "probe acme/service@main", "probe acme/service@dev"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v: one probe per reference, however many rows stand at it", f.calls, want)
	}
}

// Expedited work still waits out the cycle interval unless something wakes discovery: test-repo-3's
// first workflow sat requeued 58 s with the loop idle. A delivery that queues work wakes it, and one
// that changes nothing does not, or every probe would cost a cycle.
func TestADeliveryThatQueuesWorkWakesDiscovery(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "main", 1, shaA)
	var woken int
	p := f.processor()
	p.Wake = func() { woken++ }
	drain := func() {
		t.Helper()
		if _, err := p.Drain(context.Background()); err != nil {
			t.Fatalf("Drain: %v", err)
		}
	}

	f.answers["acme/service@"] = shaA
	f.answers["acme/service@main"] = shaA
	f.deliver("unchanged", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaA))
	f.deliver("not-ours", "push", pushPayload("other/thing", "refs/heads/main", shaA, shaB))
	drain()
	if woken != 0 {
		t.Errorf("woken %d times by deliveries that queued nothing", woken)
	}

	f.answers["acme/service@"] = shaB
	f.answers["acme/service@main"] = shaB
	f.deliver("moved", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drain()
	if woken == 0 {
		t.Error("a push that requeued two rows did not wake discovery")
	}

	before := woken
	f.branches["acme/service"] = []string{"main", "release"}
	f.deliver("protected", "branch_protection_rule", protectionRulePayload("acme/service", "created", "release"))
	drain()
	if woken == before {
		t.Error("a protection change that queued release did not wake discovery")
	}
}

// A repository read with nothing to analyze is settled at the commit it was read at, so a push is
// compared against that commit: one that left the branch where it was is a probe and nothing
// more, and one that moved it is a change from nothing, not a failure coming back.
func TestAPushToARepositoryWithNothingToAnalyzeIsComparedAgainstItsCommit(t *testing.T) {
	t.Run("the branch is where it was read", func(t *testing.T) {
		f := newFixture(t)
		if err := f.queue.Nothing(frontier.Key{NodeKey: "acme/service"}, "", shaA, "no workflows"); err != nil {
			t.Fatal(err)
		}
		f.answers["acme/service@"] = shaA

		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaA))
		f.drain()

		f.settled("d-1", store.EventDone, ReasonUnchanged)
		row := f.row("acme/service", "")
		if row.Status != frontier.StatusNothing || row.CheckedAt == "" || row.Expedited {
			t.Errorf("row = %+v, want it left with nothing to analyze and the probe recorded", row)
		}
	})

	t.Run("the branch moved", func(t *testing.T) {
		f := newFixture(t)
		if err := f.queue.Nothing(frontier.Key{NodeKey: "acme/service"}, "", shaA, "no workflows"); err != nil {
			t.Fatal(err)
		}
		f.answers["acme/service@"] = shaB

		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
		f.drain()

		f.settled("d-1", store.EventDone, ReasonRequeued)
		row := f.row("acme/service", "")
		if row.Status != frontier.StatusPending || !row.Expedited {
			t.Errorf("row = %+v, want it read again ahead of the ranking", row)
		}
	})
}
