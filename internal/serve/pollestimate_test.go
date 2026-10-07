package serve

import (
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// seedPolled holds n external references that can move, each run at its version by reach starting
// points; a reach below zero leaves the node unmeasured.
func seedPolled(t *testing.T, s *store.Store, prefix string, n, reach int) {
	t.Helper()
	sha := strings.Repeat("c", 40)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		key := fmt.Sprintf("%s/action-%d", prefix, i)
		exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha, analyzed_ref)
			VALUES (?, 'v1', ?, ?, 1, 'done', '2026-10-01T00:00:00Z', ?, 'v1')`, key, prefix, fmt.Sprintf("action-%d", i), sha)
		if reach < 0 {
			continue
		}
		exec(`INSERT INTO abom_node (node_key, node_kind) VALUES (?, 'action')`, key)
		exec(`INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_workflows, max_depth)
			SELECT row_id, ?, 0, 1 FROM abom_node WHERE node_key = ?`, reach, key)
		for r := range reach {
			repo := fmt.Sprintf("me/repo-%d", r)
			exec(`INSERT OR IGNORE INTO repository (repository_full_name, origin) VALUES (?, 'seed')`, repo)
			exec(`INSERT INTO abom_node_version_reach (node_row_id, commit_sha, repository_row_id, hops)
				SELECT n.row_id, ?, r.row_id, 1 FROM abom_node n, repository r
				 WHERE n.node_key = ? AND r.repository_full_name = ?`, sha, key, repo)
		}
	}
}

func pollingEstimate(t *testing.T, srv *Server) poolBody {
	t.Helper()
	return decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolPolling, work.UnitGraphQL)
}

// Polling is metered now, so its rate can be set and its estimate is derived like any other.
func TestThePollingEstimateSaysWhatItCannotKnow(t *testing.T) {
	srv, s := pooledServer(t)

	if got := pollingEstimate(t, srv); got.Unmetered != "" || !strings.Contains(got.NoEstimate, "nothing to ask about") {
		t.Errorf("with nothing to poll there is nothing to estimate; got %+v", got)
	}
	seedPolled(t, s, "vendor", 3, 1)
	if got := pollingEstimate(t, srv); got.Estimate != nil || !strings.Contains(got.NoEstimate, "No rate is set") {
		t.Errorf("with no rate set, no time follows from one; got %+v", got)
	}
}

// When the rate covers every reference within the floor, the floor is what governs, and says so.
func TestThePollingEstimateAtARateThatCoversEverything(t *testing.T) {
	srv, s := pooledServer(t)
	seedPolled(t, s, "vendor", 3, 1)
	srv.Pools.Pool(work.PoolPolling, work.UnitGraphQL).Configure(60, 1)

	got := pollingEstimate(t, srv)

	if got.Estimate == nil || got.Estimate.Seconds != 3600 || got.Estimate.Work != 3 {
		t.Fatalf("three references, one query, inside the hour no reference is asked sooner than; got %+v", got.Estimate)
	}
	for _, want := range []string{"each of the 3 external references", "within about 60 minutes", "estimate", "not a promise"} {
		if !strings.Contains(got.Estimate.Text, want) {
			t.Errorf("the estimate says %q; got %q", want, got.Estimate.Text)
		}
	}
}

// Polling is reach-ordered, so a rate too slow for everything still keeps what most starting points
// run fresh, and the estimate says down to which reach.
func TestThePollingEstimateNamesTheReachItsRateKeepsFresh(t *testing.T) {
	srv, s := pooledServer(t)
	seedPolled(t, s, "popular", 40, 3)
	seedPolled(t, s, "single", 80, 1)
	seedPolled(t, s, "unmeasured", 30, -1)
	srv.Pools.Pool(work.PoolPolling, work.UnitGraphQL).Configure(1, 1)

	got := pollingEstimate(t, srv)

	if got.Estimate == nil || got.Estimate.Work != 150 || got.Estimate.Seconds != 2*3600 {
		t.Fatalf("150 references are two queries, two hours at one an hour; got %+v", got.Estimate)
	}
	for _, want := range []string{
		"references used by 3 or more starting points are asked about again within about 60 minutes",
		"all 150 external references that can move, within about 2 hours",
		"An estimate from the rate, not a promise",
	} {
		if !strings.Contains(got.Estimate.Text, want) {
			t.Errorf("the estimate says %q; got %q", want, got.Estimate.Text)
		}
	}
}

// An event is followed up when it arrives, so no queue stands behind its rate to estimate from.
func TestTheEventFollowUpPoolIsMeteredWithoutAnEstimate(t *testing.T) {
	srv, _ := pooledServer(t)

	got := decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolEvents, work.UnitGit)

	if got.Unmetered != "" || got.Estimate != nil || !strings.Contains(got.NoEstimate, "when it arrives") {
		t.Errorf("event follow-up is metered and says why it has no estimate; got %+v", got)
	}
}
