package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// levels is what a node's stored reach says depends on it, in total and for one repository.
type levels struct{ repos, steps, jobs, workflows int }

func storedLevels(t *testing.T, s *store.Store, key string) levels {
	t.Helper()
	var l levels
	if err := s.DB().QueryRow(`SELECT b.dependent_repos, b.dependent_steps, b.dependent_jobs,
		       b.dependent_workflows FROM abom_blast_radius b
		  JOIN abom_node n ON n.row_id = b.node_row_id WHERE n.node_key = ?`, key).
		Scan(&l.repos, &l.steps, &l.jobs, &l.workflows); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return l
}

func storedLevelsSummed(t *testing.T, s *store.Store, key string) levels {
	t.Helper()
	var l levels
	if err := s.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(r.steps), 0), COALESCE(SUM(r.jobs), 0),
		       COALESCE(SUM(r.workflows), 0) FROM abom_node_reach r
		  JOIN abom_node n ON n.row_id = r.node_row_id WHERE n.node_key = ?`, key).
		Scan(&l.repos, &l.steps, &l.jobs, &l.workflows); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return l
}

// usesComp declares one step of a repository's workflow naming vendor/comp at a reference.
func (f *releaseFixture) usesComp(t *testing.T, repo int64, workflow, job, step, ref string) {
	t.Helper()
	f.node(t, store.Node{Key: step, Kind: "step", RepositoryID: repo, ContainerKey: workflow, JobID: job})
	f.flush(t)
	if err := f.w.AddEdge(store.Edge{Key: step + ":edge", SrcKey: step, DstKey: "vendor/comp",
		Kind: store.EdgeKindStepUsesAction, RefRaw: "vendor/comp@" + ref, RefValue: ref,
		RepositoryID: repo, FilePath: workflow, StartLine: len(step), StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
}

// What depends on a node is counted at every level a reader manages: the steps that run it, the
// jobs holding them, the workflow files holding those, and the repositories. Only starting points
// count, through another action as much as directly, and each level follows the commit a step runs.
func TestDependentsAreCountedAtEveryLevelAtTheCommitEachStepRuns(t *testing.T) {
	f := newReleaseFixture(t)
	levelsRepo, err := f.s.UpsertRepository("octo/levels", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"octo/levels/ci.yml", "octo/levels/release.yml"} {
		f.node(t, store.Node{Key: w, Kind: "workflow", RepositoryID: levelsRepo})
	}
	f.flush(t)
	f.usesComp(t, levelsRepo, "octo/levels/ci.yml", "build", "build-1", "v1")
	f.usesComp(t, levelsRepo, "octo/levels/ci.yml", "build", "build-2", "v2")
	f.usesComp(t, levelsRepo, "octo/levels/ci.yml", "test", "test-1", "v1")
	f.usesComp(t, levelsRepo, "octo/levels/release.yml", "ship", "ship-1", "v1")
	upstream, err := f.s.InsertUpstreamRepository("third/party")
	if err != nil {
		t.Fatal(err)
	}
	f.node(t, store.Node{Key: "third/party/ci.yml", Kind: "workflow", RepositoryID: upstream})
	f.flush(t)
	f.usesComp(t, upstream, "third/party/ci.yml", "build", "third-1", "v1")
	// A job calling a shared workflow depends on what that workflow runs without a step of its own.
	f.node(t, store.Node{Key: "third/party/shared.yml", Kind: "reusable_workflow", RepositoryID: upstream})
	f.node(t, store.Node{Key: "octo/levels/deploy.yml", Kind: "workflow", RepositoryID: levelsRepo})
	f.node(t, store.Node{Key: "deploy", Kind: "job", RepositoryID: levelsRepo,
		ContainerKey: "octo/levels/deploy.yml", JobID: "deploy"})
	f.flush(t)
	f.usesComp(t, upstream, "third/party/shared.yml", "shared", "shared-1", "v1")
	if err := f.w.AddEdge(store.Edge{Key: "deploy:edge", SrcKey: "deploy", DstKey: "third/party/shared.yml",
		Kind: store.EdgeKindJobUsesReusableWorkflow, RefRaw: "third/party/shared.yml@v1", RefValue: "v1",
		RepositoryID: levelsRepo, FilePath: "octo/levels/deploy.yml", StartLine: 7, StartColumn: 11}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if err := (&Builder{Store: f.s}).BuildBlastRadius(); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]levels{
		"vendor/comp":      {repos: 1, steps: 4, jobs: 4, workflows: 3},
		"actions/cache":    {repos: 1, steps: 3, jobs: 4, workflows: 3},
		"actions/setup-go": {repos: 1, steps: 1, jobs: 1, workflows: 1},
	} {
		if got := storedLevels(t, f.s, key); got != want {
			t.Errorf("%s depends on %+v, want %+v", key, got, want)
		}
		if got := storedLevelsSummed(t, f.s, key); got != want {
			t.Errorf("%s sums to %+v by repository, want %+v", key, got, want)
		}
	}
}
