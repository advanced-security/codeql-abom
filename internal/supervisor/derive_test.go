package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// arrives records a starting point naming an action, as an ingest would.
func arrives(t *testing.T, s *store.Store, repository, action string) {
	t.Helper()
	repo, err := s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	workflow := repository + "/.github/workflows/ci.yml"
	owner, name, _ := strings.Cut(action, "/")
	for _, n := range []store.Node{
		{Key: action, Kind: "action", Owner: owner, Repo: name, IsExternal: true},
		{Key: workflow, Kind: "workflow", RepositoryID: repo},
		{Key: repository + "-step", Kind: "step", RepositoryID: repo, ContainerKey: workflow, JobID: "ci"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(store.Edge{Key: repository + ":ci", SrcKey: repository + "-step", DstKey: action,
		Kind: store.EdgeKindStepUsesAction, RefRaw: action + "@v4", RefValue: "v4", RepositoryID: repo,
		FilePath: ".github/workflows/ci.yml", StartLine: 7, StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func dependentRepos(t *testing.T, s *store.Store, key string) (int, bool) {
	t.Helper()
	var n int
	err := s.DB().QueryRow(`SELECT b.dependent_repos FROM abom_blast_radius b
		JOIN abom_node n ON n.row_id = b.node_row_id WHERE n.node_key = ?`, key).Scan(&n)
	if err != nil {
		return 0, false
	}
	return n, true
}

// What arrived is measured before the next rebuild, which as the oracle keeps its own schedule.
func TestACycleDerivesReachForWhatArrivedWithoutWaitingForARebuild(t *testing.T) {
	sup, s := newSupervisor(t)
	sup.Aggregates = AggregatePolicy{MinInterval: time.Hour}
	arrives(t, s, "octo/app", "actions/checkout")
	if err := sup.refreshAggregates(false, true); err != nil {
		t.Fatalf("the first refresh: %v", err)
	}
	rebuildReason := sup.Status().LastRebuildReason
	if rebuildReason == "" {
		t.Fatal("reach was never measured and the first refresh did not rebuild it")
	}

	arrives(t, s, "octo/web", "actions/setup-node")
	if err := sup.refreshAggregates(false, true); err != nil {
		t.Fatalf("the second refresh: %v", err)
	}

	if repos, ok := dependentRepos(t, s, "actions/setup-node"); !ok || repos != 1 {
		t.Errorf("actions/setup-node arrived and reach says %d dependents (measured %v), want octo/web",
			repos, ok)
	}
	if got := sup.Status(); got.ReachPending != 0 || got.LastRebuildReason != rebuildReason {
		t.Errorf("status %+v: nothing should be pending, and the last rebuild is still the first", got)
	}
	var incremental bool
	if err := s.DB().QueryRow(`SELECT reach_rebuilt = 1 AND reach_incremental = 1
		FROM abom_graph_generation ORDER BY generation DESC LIMIT 1`).Scan(&incremental); err != nil {
		t.Fatal(err)
	}
	if !incremental {
		t.Error("the latest generation does not record reach as derived incrementally")
	}
	st, err := s.ReachStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if st.NewEdges != 1 {
		t.Errorf("the rebuild schedule counts %d arrivals since the rebuild, want 1", st.NewEdges)
	}
}

// Reach written without a mark, by an older build or a partial pass, cannot say what changed since.
func TestACycleRebuildsReachThatCarriesNoMark(t *testing.T) {
	sup, s := newSupervisor(t)
	sup.Aggregates = AggregatePolicy{MinInterval: time.Hour}
	arrives(t, s, "octo/app", "actions/checkout")
	if err := sup.Builder.BuildBlastRadius(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGraphGeneration(store.GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}

	if err := sup.refreshAggregates(false, false); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if why := sup.Status().LastRebuildReason; !strings.Contains(why, "mark") {
		t.Errorf("last rebuild reason %q; reach carried no mark, so the cycle should have rebuilt", why)
	}
	if _, ok, err := s.ReadReachMark(); err != nil || !ok {
		t.Errorf("after the rebuild reach still carries no mark (%v)", err)
	}
}
