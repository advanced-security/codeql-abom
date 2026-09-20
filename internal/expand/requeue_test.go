package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// boundToOnePack binds a real pack to both tiers, so the identity resolves to something a
// recorded run can be compared against.
func boundToOnePack(t *testing.T) (*harness, string) {
	t.Helper()
	root := t.TempDir()
	query := queryInPack(t, root, "advanced-security/abom-graph", "2.26.4", "Edges.ql", "select 1")

	h := newHarness(t)
	h.expander.Resolver = &packedResolver{queries: map[string][]string{"graph.qls": {query}}}
	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		if err := h.store.SetTierSuites(tier, []string{"graph.qls"}); err != nil {
			t.Fatalf("SetTierSuites(%s): %v", tier, err)
		}
	}

	_, digests, err := h.expander.CurrentIdentity(context.Background())
	if err != nil {
		t.Fatalf("CurrentIdentity: %v", err)
	}
	if len(digests) != 1 {
		t.Fatalf("got %d digests, want the one bound pack", len(digests))
	}
	return h, digests[0]
}

func settledRow(t *testing.T, h *harness, owner, repo string) {
	t.Helper()
	if _, err := h.store.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at)
		VALUES (?,?,?,1,'done',1,'2026-01-01')`, owner+"/"+repo, owner, repo); err != nil {
		t.Fatalf("seeding %s/%s: %v", owner, repo, err)
	}
}

func analyzedAt(t *testing.T, h *harness, repo, digest, cliVersion string) {
	t.Helper()
	if _, err := h.store.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "abom/edges", Repository: repo, DBCommit: "sha",
		Pack: "graph", PackVersion: "1.0.0", PackDigest: digest, CLIVersion: cliVersion,
	}); err != nil {
		t.Fatalf("RecordQueryRun for %s: %v", repo, err)
	}
}

func statusOfRow(t *testing.T, h *harness, key string) string {
	t.Helper()
	var status string
	if err := h.store.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key=?`, key).Scan(&status); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return status
}

// Staleness is judged against what the bound suites resolve to now, and the answer has to travel
// with the count: "1,742 requeued" alone cannot be checked by anyone, where "against CodeQL
// 2.26.4 and 1 pack" can.
func TestSelectingStaleWorkReportsWhatItJudgedAgainst(t *testing.T) {
	h, digest := boundToOnePack(t)
	settledRow(t, h, "octo", "current")
	settledRow(t, h, "octo", "stale")
	analyzedAt(t, h, "octo/current", digest, "2.26.4")

	got, err := h.expander.RequeueStale(context.Background())
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}

	if got.Requeued != 1 {
		t.Errorf("requeued %d, want only the repository missing the current pack", got.Requeued)
	}
	if got.CLIVersion != "2.26.4" || got.Packs != 1 {
		t.Errorf("judged against CodeQL %q and %d packs, want 2.26.4 and 1",
			got.CLIVersion, got.Packs)
	}
	if s := statusOfRow(t, h, "octo/current"); s != "done" {
		t.Errorf("a repository at the current identity is %q, want left done", s)
	}
	if s := statusOfRow(t, h, "octo/stale"); s != "pending" {
		t.Errorf("the stale repository is %q, want pending", s)
	}
}

// Nothing bound means nothing resolves, which is a server that cannot read its packs rather than
// a corpus that is entirely out of date. Requeueing everything is a very expensive way to report
// that, so it is refused before it reaches the queue.
func TestAnIdentityThatResolvesToNothingRequeuesNothing(t *testing.T) {
	h := newHarness(t)
	settledRow(t, h, "octo", "one")

	got, err := h.expander.RequeueStale(context.Background())
	if err == nil {
		t.Fatal("an unresolvable identity was accepted")
	}
	if got.Requeued != 0 {
		t.Errorf("requeued %d on a refused call", got.Requeued)
	}
	if s := statusOfRow(t, h, "octo/one"); s != "done" {
		t.Errorf("octo/one is %q, want untouched", s)
	}
}

// Someone who has just fixed a dependency they own is asking for two things: run it again, and
// run it now. Requeueing alone files the request behind everything already queued, which refuses
// it in all but name.
func TestRequeueingARepositoryPutsItAheadOfTheRanking(t *testing.T) {
	h := newHarness(t)
	settledRow(t, h, "octo", "mine")

	got, err := h.expander.RequeueRepository("octo/mine")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}

	if got.Requeued != 1 || got.Expedited != 1 {
		t.Errorf("requeued %d and expedited %d, want 1 and 1", got.Requeued, got.Expedited)
	}
	var marked int
	if err := h.store.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE expedited_at IS NOT NULL`).Scan(&marked); err != nil {
		t.Fatalf("reading expedited_at: %v", err)
	}
	if marked != 1 {
		t.Errorf("%d rows marked to run ahead of the ranking, want 1", marked)
	}
}

// "Nothing by that name" and "requeued nothing" are the same number and different answers. The
// caller has to be able to tell them apart, or an operator waits on a crawl that was never going
// to reach what they asked about.
func TestRequeueingARepositoryNothingKnowsAboutIsNotAnError(t *testing.T) {
	h := newHarness(t)
	settledRow(t, h, "octo", "mine")

	got, err := h.expander.RequeueRepository("octo/typo")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if got.Requeued != 0 || got.Expedited != 0 {
		t.Errorf("requeued %d and expedited %d for a repository nothing knows about",
			got.Requeued, got.Expedited)
	}
	if s := statusOfRow(t, h, "octo/mine"); s != "done" {
		t.Errorf("octo/mine is %q, want untouched", s)
	}
}

// A repository already in the queue has nothing to put back, but the request is still "run this
// next" and can still be answered. Found on the real corpus: a stale selection had already
// requeued every row of the repository asked for, so the only reply left was "nothing analyzed
// for it", which is wrong twice over.
func TestARepositoryAlreadyQueuedIsStillMovedAheadOfTheRanking(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "mine", "", 1))

	got, err := h.expander.RequeueRepository("octo/mine")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if got.Requeued != 0 {
		t.Errorf("requeued %d, want 0: it was already queued", got.Requeued)
	}
	if got.Expedited != 1 {
		t.Errorf("expedited %d, want the queued row moved ahead of the ranking", got.Expedited)
	}
}
