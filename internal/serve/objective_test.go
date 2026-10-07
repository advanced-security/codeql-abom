package serve

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// analyzedBeforeContent is vendor/comp analyzed at each commit before sightings kept content: an
// edges run at each, none marked, and a finished queue row whose reference resolved there.
func analyzedBeforeContent(t *testing.T, s *store.Store, commits ...string) {
	t.Helper()
	repo, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "vendor/comp", Kind: "composite_action", Owner: "vendor", Repo: "comp",
			RepositoryID: repo, Path: "action.yml"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for i, commit := range commits {
		run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: "vendor/comp", DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(store.Edge{Key: "vendor/comp:action.yml:9:9", SrcKey: "vendor/comp",
			DstKey: "actions/cache", Kind: store.EdgeKindCompositeStepUsesAction,
			RefRaw: "actions/cache@v4", RefValue: "v4", RepositoryID: repo, QueryRunID: run,
			FilePath: "action.yml", StartLine: 9, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref, analyzed_at, analysis_count)
			VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done', '2026-09-20T00:00:00Z', ?, ?,
			        '2026-09-21T00:00:00Z', 1)`,
			fmt.Sprintf("v%d", i+1), commit, fmt.Sprintf("v%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
}

// Re-observing what was recorded before sightings kept content is part of what the crawl is working
// on, and how much is left is a gap in what is known, not a detail of the queue. Zero is measured
// here rather than absent: the count is taken on every request.
func TestTheObjectiveSaysHowManyCommitsAreNotYetObserved(t *testing.T) {
	srv, s := newServer(t)
	unobserved := func() any {
		t.Helper()
		rec := get(t, srv, "/api/discovery/objective")
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body.String(), err)
		}
		return body["unobserved"]
	}
	if got := unobserved(); got != float64(0) {
		t.Errorf("an empty store reports unobserved = %v, want 0", got)
	}

	analyzedBeforeContent(t, s, "c1", "c2")

	if got := unobserved(); got != float64(2) {
		t.Errorf("unobserved = %v, want vendor/comp's 2 commits", got)
	}
}

func failedAttempts(t *testing.T, s *store.Store, key string, depth, attempts int) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, attempts, reason,
		                           enqueued_at, next_attempt_at)
		VALUES (?, '', 'octo', ?, ?, 'pending', ?, 'exit status 32', '2026-09-20T00:00:00Z',
		        '2999-01-01T00:00:00Z')`, key, key, depth, attempts); err != nil {
		t.Fatal(err)
	}
}

type objectiveBody struct {
	Progress []struct {
		Depth   int `json:"depth"`
		Total   int `json:"total"`
		Done    int `json:"done"`
		Settled int `json:"settled"`
		Stuck   int `json:"stuck"`
	} `json:"progress"`
	HasOutstanding bool `json:"hasOutstanding"`
	StuckAfter     int  `json:"stuckAfter"`
}

// Without the stuck count and its ceiling, "nothing outstanding" claims stuck rows were analyzed.
func TestTheObjectiveReportsWorkStuckAfterRepeatedFailures(t *testing.T) {
	srv, s := newServer(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('octo/fine', '', 'octo', 'fine', 0, 'done', '2026-09-20T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	failedAttempts(t, s, "octo/bootcamp", 0, 10)

	got := decode[objectiveBody](t, get(t, srv, "/api/discovery/objective"))

	if got.StuckAfter != 8 {
		t.Errorf("stuckAfter = %d, want the default ceiling of 8", got.StuckAfter)
	}
	if got.HasOutstanding {
		t.Error("a depth holding only stuck work is reported outstanding")
	}
	if len(got.Progress) != 1 {
		t.Fatalf("progress = %+v, want depth 0", got.Progress)
	}
	if d := got.Progress[0]; d.Stuck != 1 || d.Done != 1 || d.Settled != 1 || d.Total != 2 {
		t.Errorf("depth 0 = %+v, want 1 analyzed and 1 stuck of 2, the stuck one not settled", d)
	}
}

type ceilingDiscovery struct {
	*fakeDiscovery
	stuckAfter int
}

func (c ceilingDiscovery) Status() supervisor.Status {
	return supervisor.Status{State: supervisor.StatePaused, StuckAfter: c.stuckAfter}
}

// The ceiling is the loop's, so the objective counts by the one the running crawl judges with.
func TestTheObjectiveCountsStuckWorkByTheLoopsCeiling(t *testing.T) {
	srv, s := newServer(t)
	srv.Discovery = ceilingDiscovery{fakeDiscovery: &fakeDiscovery{}, stuckAfter: 3}
	failedAttempts(t, s, "octo/bootcamp", 0, 3)

	got := decode[objectiveBody](t, get(t, srv, "/api/discovery/objective"))

	if got.StuckAfter != 3 || len(got.Progress) != 1 || got.Progress[0].Stuck != 1 {
		t.Errorf("objective = %+v, want 1 stuck under the loop's ceiling of 3", got)
	}
}

type stagedObjective struct {
	Progress []struct {
		Depth   int `json:"depth"`
		Total   int `json:"total"`
		Refresh int `json:"refresh"`
	} `json:"progress"`
	HasOutstanding bool `json:"hasOutstanding"`
	Working        int  `json:"working"`
	Stages         struct {
		Next      string `json:"next"`
		Expedited int    `json:"expedited"`
		Current   int    `json:"current"`
		Explore   int    `json:"explore"`
		Refresh   int    `json:"refresh"`
		Unreached int    `json:"unreached"`
	} `json:"stages"`
	Unobserved int `json:"unobserved"`
}

// The objective is the crawl's, so it is measured inside the closure the crawl claims in: work
// outside is unreached rather than outstanding, and a moved reference is the third stage's rather than
// a reopened depth.
func TestTheObjectiveNamesTheStageAndWhatTheClosureDoesNotReach(t *testing.T) {
	srv, s := newServer(t)
	analyzedBeforeContent(t, s, "c1", "c2")
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           analysis_count, analyzed_at, requeued_for)
		VALUES ('octo/new', '', 'octo', 'new', 2, 'pending', '2026-09-20T00:00:00Z', 0, NULL, NULL),
		       ('octo/moved', '', 'octo', 'moved', 1, 'pending', '2026-09-20T00:00:00Z', 1,
		        '2026-09-01T00:00:00Z', 'moved'),
		       ('left/behind', '', 'left', 'behind', 1, 'pending', '2026-09-20T00:00:00Z', 0, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	srv.QueueClosure = func() ([]string, error) { return []string{"octo/new", "octo/moved"}, nil }

	got := decode[stagedObjective](t, get(t, srv, "/api/discovery/objective"))

	if !got.HasOutstanding || got.Working != 2 {
		t.Errorf("working = %d, outstanding = %v; want depth 2, the one never analyzed",
			got.Working, got.HasOutstanding)
	}
	if st := got.Stages; st.Next != "current" || st.Current != 1 || st.Refresh != 1 || st.Unreached != 1 {
		t.Errorf("stages = %+v, want current next, 1 current, 1 refresh, 1 unreached", st)
	}
	if got.Unobserved != 0 {
		t.Errorf("unobserved = %d, want 0: vendor/comp is outside the closure", got.Unobserved)
	}
	for _, d := range got.Progress {
		if d.Depth == 1 && (d.Total != 1 || d.Refresh != 1) {
			t.Errorf("depth 1 = %+v, want only octo/moved, being refreshed", d)
		}
	}
}
