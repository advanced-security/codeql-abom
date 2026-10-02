package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// queueState is one queue row as re-observation leaves it.
type queueState struct {
	status, reason, resolved, analyzedAt string
	attempts, analysisCount              int
}

func reobservedRow(t *testing.T, s *Store, nodeKey, ref string) queueState {
	t.Helper()
	var q queueState
	var reason sql.NullString
	if err := s.DB().QueryRow(`SELECT status, reason, COALESCE(resolved_sha, ''),
	       COALESCE(analyzed_at, ''), attempts, analysis_count
	  FROM abom_frontier WHERE node_key = ? AND ref = ?`, nodeKey, ref).
		Scan(&q.status, &reason, &q.resolved, &q.analyzedAt, &q.attempts, &q.analysisCount); err != nil {
		t.Fatalf("reading %s@%s: %v", nodeKey, ref, err)
	}
	q.reason = reason.String
	return q
}

// analyzedBeforeContent is a repository a crawl analyzed at each commit before sightings kept
// content: an edges run there, unmarked, and a finished queue row whose reference resolved to it.
// Each commit's reference is "v" followed by its position, and the rows share depth and score.
func analyzedBeforeContent(t *testing.T, s *Store, repository string, depth int, score float64,
	commits ...string) map[string]int64 {
	t.Helper()
	repo, err := s.InsertUpstreamRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	owner, name, _ := strings.Cut(repository, "/")
	step := repository + "-step"
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: repository, Kind: "action", Owner: owner, Repo: name, IsExternal: true},
		{Key: step, Kind: "step", RepositoryID: repo, ContainerKey: repository},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	runs := map[string]int64{}
	for i, commit := range commits {
		run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
			Repository: repository, DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		runs[commit] = run
		if err := w.AddEdge(Edge{Key: repository + ":action.yml:9:9", SrcKey: step,
			DstKey: "actions/cache", Kind: EdgeKindCompositeStepUsesAction, RefRaw: "actions/cache@v4",
			RefValue: "v4", RepositoryID: repo, QueryRunID: run, FilePath: "action.yml", StartLine: 9,
			StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		ref := fmt.Sprintf("v%d", i+1)
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, score, enqueued_at,
			                           completed_at, resolved_sha, analyzed_ref, analyzed_at,
			                           analysis_count)
			VALUES (?, ?, ?, ?, ?, 'done', ?, '2026-09-20T00:00:00Z', '2026-09-21T00:00:00Z', ?, ?,
			        '2026-09-21T00:00:00Z', 1)`,
			repository, ref, owner, name, depth, score, commit, ref); err != nil {
			t.Fatal(err)
		}
	}
	return runs
}

func reobserve(t *testing.T, s *Store, window int) Reobservation {
	t.Helper()
	got, err := s.Reobserve(window)
	if err != nil {
		t.Fatalf("Reobserve(%d): %v", window, err)
	}
	return got
}

// A commit analyzed before sightings kept content shares rows with every other commit of its
// repository, so walks through it may lack a route it declares and a submission reading it is
// refused. Analyzing it again is the only way to learn what it declared, and putting its row back is
// how the crawl meets it. Everything already on record stays, because the walks keep reading it
// until the new run replaces it: a re-observation is not an outage.
func TestDiscoveryPutsBackADependencyWhoseCommitsDeclarationsAreNotKnown(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	analyzedBeforeContent(t, s, "vendor/solo", 1, 1, "s1")
	// The objective counts on every read, so counting must put nothing back.
	if n, err := s.UnobservedCommitCount(); err != nil || n != 2 {
		t.Errorf("count = %d, %v; want vendor/comp's 2 commits", n, err)
	}
	if row := reobservedRow(t, s, "vendor/comp", "v1"); row.status != "done" {
		t.Errorf("counting put a row back: %+v", row)
	}

	got := reobserve(t, s, 10)

	if got.Unobserved != 2 || got.Requeued != 2 || got.Outstanding != 0 {
		t.Errorf("Reobserve = %+v, want 2 unobserved, 2 put back, none outstanding", got)
	}
	for ref, commit := range map[string]string{"v1": "c1", "v2": "c2"} {
		row := reobservedRow(t, s, "vendor/comp", ref)
		if row.status != "pending" || row.attempts != 0 {
			t.Errorf("vendor/comp@%s is %s with %d attempts, want pending and untried",
				ref, row.status, row.attempts)
		}
		if !strings.Contains(row.reason, commit) {
			t.Errorf("vendor/comp@%s says %q, which does not name the commit %s being re-observed",
				ref, row.reason, commit)
		}
		if row.resolved != commit || row.analyzedAt == "" || row.analysisCount != 1 {
			t.Errorf("vendor/comp@%s lost what is on record: %+v", ref, row)
		}
	}
	if row := reobservedRow(t, s, "vendor/solo", "v1"); row.status != "done" {
		t.Errorf("vendor/solo, analyzed at its only commit, was put back: %+v", row)
	}
}

// Once an edges run of a commit is marked, what that commit declared is known, and analyzing it
// again would buy nothing. Knowledge is per commit, so the other commit of the same repository is
// still put back.
func TestOnlyCommitsStillUnobservedArePutBack(t *testing.T) {
	s := newTestStore(t)
	runs := analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	if err := s.MarkSightedContent(runs["c1"]); err != nil {
		t.Fatal(err)
	}

	got := reobserve(t, s, 10)

	if got.Unobserved != 1 || got.Requeued != 1 {
		t.Errorf("Reobserve = %+v, want only c2 unobserved and put back", got)
	}
	if row := reobservedRow(t, s, "vendor/comp", "v1"); row.status != "done" {
		t.Errorf("vendor/comp@v1, whose commit is observed, is %s", row.status)
	}
	if row := reobservedRow(t, s, "vendor/comp", "v2"); row.status != "pending" {
		t.Errorf("vendor/comp@v2, whose commit is not, is %s", row.status)
	}

	if err := s.MarkSightedContent(runs["c2"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET status = 'done'`); err != nil {
		t.Fatal(err)
	}
	if got := reobserve(t, s, 10); got.Unobserved != 0 || got.Requeued != 0 {
		t.Errorf("with every commit observed Reobserve = %+v, want nothing", got)
	}
}

// Putting every unobserved commit back at once is a re-crawl: on the crawled organization it was
// 2,456 rows at every start. Analyzed work back in the queue is bounded instead, by the window the
// caller gives, so new work keeps its place and each cycle has only so much to re-observe. The
// shallowest commits go first, the most used within a depth, which is the order the crawl claims.
func TestReobservationKeepsAWindowOfAnalyzedWorkInTheQueueShallowestAndMostUsedFirst(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/deep", 3, 9, "d1", "d2")
	analyzedBeforeContent(t, s, "vendor/rare", 2, 1, "r1", "r2")
	analyzedBeforeContent(t, s, "vendor/used", 2, 5, "u1", "u2")
	near := analyzedBeforeContent(t, s, "vendor/near", 1, 1, "n1", "n2")

	got := reobserve(t, s, 3)
	if got.Unobserved != 8 || got.Requeued != 3 || got.Outstanding != 0 {
		t.Fatalf("Reobserve = %+v, want 8 unobserved and a window of 3 put back", got)
	}
	pending := func() []string {
		t.Helper()
		rows, err := s.DB().Query(`SELECT node_key || '@' || ref FROM abom_frontier
			WHERE status = 'pending' ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			out = append(out, k)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got, want := strings.Join(pending(), " "), "vendor/near@v1 vendor/near@v2 vendor/used@v1"; got != want {
		t.Errorf("put back %s, want %s: the shallowest first, then the most used", got, want)
	}

	if got := reobserve(t, s, 3); got.Requeued != 0 || got.Outstanding != 3 {
		t.Errorf("with the window full Reobserve = %+v, want nothing more put back", got)
	}

	if err := s.MarkSightedContent(near["n1"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET status = 'done'
		WHERE node_key = 'vendor/near' AND ref = 'v1'`); err != nil {
		t.Fatal(err)
	}
	got = reobserve(t, s, 3)
	if got.Requeued != 1 || got.Outstanding != 2 || got.Unobserved != 7 {
		t.Errorf("with one re-observed Reobserve = %+v, want one more put back of 7 unobserved", got)
	}
	if got, want := strings.Join(pending(), " "),
		"vendor/near@v2 vendor/used@v1 vendor/used@v2"; got != want {
		t.Errorf("put back %s, want %s", got, want)
	}

	if got := reobserve(t, s, 0); got.Requeued != 0 || got.Unobserved != 7 {
		t.Errorf("with no window Reobserve = %+v, want the backlog measured and nothing put back", got)
	}
}

// Several references of one action resolve to one commit, and so do several actions in one
// repository. Analyzing the commit once answers all of them, so the commit is put back once: a
// second row would analyze the same commit twice in one cycle.
func TestACommitIsPutBackOnceHoweverManyRowsResolveToIt(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, score, enqueued_at,
		                           resolved_sha, analyzed_ref, analyzed_at, analysis_count)
		VALUES ('vendor/comp', 'v1.0.0', 'vendor', 'comp', 1, 'done', 1, '2026-09-20T00:00:00Z',
		        'c1', 'v1.0.0', '2026-09-21T00:00:00Z', 1),
		       ('vendor/comp/sub', 'v1', 'vendor', 'comp', 1, 'done', 1, '2026-09-20T00:00:00Z',
		        'c1', 'v1', '2026-09-21T00:00:00Z', 1)`); err != nil {
		t.Fatal(err)
	}

	got := reobserve(t, s, 10)

	if got.Unobserved != 2 || got.Requeued != 2 {
		t.Errorf("Reobserve = %+v, want each of 2 commits put back once", got)
	}
	var c1 int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier
		WHERE status = 'pending' AND resolved_sha = 'c1'`).Scan(&c1); err != nil {
		t.Fatal(err)
	}
	if c1 != 1 {
		t.Errorf("%d rows resolving to c1 were put back, want 1", c1)
	}
}

// A commit already in the queue is already being met: a row moved back by a recheck, an operator's
// requeue or an earlier pass will analyze it. Putting back another row resolving to it would only
// analyze it twice. Work that has never been analyzed is not re-observation and takes no room.
func TestWorkAlreadyQueuedIsLeftAloneAndAnalyzedWorkTakesTheRoom(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	analyzedBeforeContent(t, s, "vendor/other", 1, 1, "o1", "o2")
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET status = 'pending', reason = 'ref moved to c9'
		WHERE node_key = 'vendor/comp' AND ref = 'v1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('vendor/new', '', 'vendor', 'new', 1, 'pending', '2026-09-22T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	got := reobserve(t, s, 3)

	if got.Outstanding != 1 || got.Requeued != 2 || got.Unobserved != 4 {
		t.Errorf("Reobserve = %+v, want 1 outstanding, 2 put back of 4 unobserved", got)
	}
	if row := reobservedRow(t, s, "vendor/comp", "v1"); row.reason != "ref moved to c9" {
		t.Errorf("the recheck's row was rewritten: %+v", row)
	}
	if row := reobservedRow(t, s, "vendor/new", ""); row.status != "pending" || row.analysisCount != 0 {
		t.Errorf("never-analyzed work was touched: %+v", row)
	}
}

// A re-observation that fails returns to the queue with a retry time, and keeps its place in the
// window. Letting more in instead would, through an outage, move every unobserved row into retrying
// one window at a time, which is the re-crawl the window exists to prevent.
func TestAFailedReobservationHoldsItsPlaceRatherThanLettingMoreIn(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	reobserve(t, s, 1)
	if _, err := s.DB().Exec(`UPDATE abom_frontier
		SET attempts = 1, next_attempt_at = '2999-01-01T00:00:00Z', reason = 'offline'
		WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}

	if got := reobserve(t, s, 1); got.Requeued != 0 || got.Outstanding != 1 {
		t.Errorf("after a failure Reobserve = %+v, want the failed row holding the window", got)
	}
}

// Unavailable is a statement somebody has to act on, never something a pass retries on its own.
// Its commit is still unobserved, and the count says so.
func TestAnUnavailableDependencyIsCountedAndNotRetried(t *testing.T) {
	s := newTestStore(t)
	analyzedBeforeContent(t, s, "vendor/comp", 1, 1, "c1", "c2")
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET status = 'unavailable'
		WHERE node_key = 'vendor/comp' AND ref = 'v2'`); err != nil {
		t.Fatal(err)
	}

	got := reobserve(t, s, 10)

	if got.Unobserved != 2 || got.Requeued != 1 {
		t.Errorf("Reobserve = %+v, want both commits counted and only c1 put back", got)
	}
	if row := reobservedRow(t, s, "vendor/comp", "v2"); row.status != "unavailable" {
		t.Errorf("an unavailable dependency was retried: %+v", row)
	}
}
