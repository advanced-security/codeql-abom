package expand

import (
	"context"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
)

// visibility answers the source policy's questions with one repository visibility for everything.
type visibility string

func (v visibility) SourceAccess(_ context.Context, repository string) (catalog.SourceAccess, error) {
	return catalog.SourceAccess{Repository: repository, Visibility: string(v)}, nil
}
func (visibility) DefaultSetup(context.Context, string) (catalog.DefaultSetup, error) {
	return catalog.DefaultSetup{}, nil
}
func (visibility) CheckScanningAccess(context.Context, string) error { return nil }

func policyOf(v visibility) *sourcepolicy.Policy {
	return &sourcepolicy.Policy{Read: v, Owned: func(string) (bool, error) { return false, nil }}
}

type remoteFunc func(ctx context.Context, t RemoteTask, dir string) (RemoteOutput, error)

func (f remoteFunc) Run(ctx context.Context, t RemoteTask, dir string) (RemoteOutput, error) {
	return f(ctx, t, dir)
}

// remoteHarness has the coordinator hand every batch to a worker built from the same fakes, which
// holds no store: a worker that read or wrote one would panic here.
func remoteHarness(t *testing.T) (*harness, *[]RemoteTask) {
	h := newHarness(t)
	h.expander.SourcePolicy = policyOf("public")
	worker := &Expander{Fetcher: h, Builder: h, CodeQL: h, Decode: h.decode}
	var tasks []RemoteTask
	h.expander.Remote = remoteFunc(func(ctx context.Context, task RemoteTask, dir string) (RemoteOutput, error) {
		tasks = append(tasks, task)
		return worker.AnalyzeTask(ctx, task, dir)
	})
	return h, &tasks
}

// A batch analyzed elsewhere settles exactly as one analyzed here: the worker fetches and runs
// CodeQL, and the coordinator records the content, the lock, the ledger and the edges.
func TestABatchAnalyzedByAWorkerSettlesAsOneAnalyzedHere(t *testing.T) {
	h, tasks := remoteHarness(t)
	h.files["gradle/actions"] = map[string]string{".github/workflows/actions.lock": "lockVersion: 0.0.3\n"}
	h.enqueue(item("gradle", "actions", "setup-gradle", 1), item("gradle", "actions", "wrapper-validation", 1))

	stats := h.run(10)

	if len(*tasks) != 1 || (*tasks)[0].Owner != "gradle" || (*tasks)[0].Repo != "actions" || len((*tasks)[0].Queries) == 0 {
		t.Fatalf("tasks = %+v, want one for gradle/actions naming its queries", *tasks)
	}
	if (*tasks)[0].Admitted != "public" {
		t.Errorf("the task says it was admitted as %q; want the source policy's reason", (*tasks)[0].Admitted)
	}
	if stats.Processed != 1 || stats.Items != 2 || len(h.fetched) != 1 || len(h.built) != 1 {
		t.Errorf("stats %+v, fetched %v, built %v", stats, h.fetched, h.built)
	}
	for _, key := range []string{"gradle/actions/setup-gradle", "gradle/actions/wrapper-validation"} {
		if status, sha, _ := statusOf(t, h.store, key); status != frontier.StatusDone || sha != "sha-actions" {
			t.Errorf("%s = %s at %s", key, status, sha)
		}
	}
	var content, runs, locks int
	for query, n := range map[string]*int{
		`SELECT COUNT(*) FROM abom_commit_content WHERE repository = 'gradle/actions'`: &content,
		`SELECT COUNT(*) FROM abom_query_run WHERE repository = 'gradle/actions'`:      &runs,
		`SELECT COUNT(*) FROM abom_lock_coverage WHERE repository = 'gradle/actions'`:  &locks,
	} {
		if err := h.store.DB().QueryRow(query).Scan(n); err != nil {
			t.Fatal(err)
		}
	}
	if content != 1 || runs == 0 || locks == 0 {
		t.Errorf("recorded content %d, runs %d, lock readings %d; want each from the worker's output", content, runs, locks)
	}

	// The same commit again: the worker's output answers nothing the ledger does not already hold.
	if _, err := h.store.DB().Exec(`UPDATE abom_frontier SET status = 'pending' WHERE owner = 'gradle'`); err != nil {
		t.Fatal(err)
	}
	if again := h.run(2); again.Skipped != 1 {
		t.Errorf("a second analysis of the same commit was not skipped from the ledger: %+v", again)
	}
	var rerun int
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_run WHERE repository = 'gradle/actions'`).Scan(&rerun); err != nil || rerun != runs {
		t.Errorf("gradle/actions has %d runs after the second analysis, want the %d it had", rerun, runs)
	}
}

// A starting point with nothing to analyze is the worker's answer to give and the coordinator's to
// settle, as it is here.
func TestAWorkersNothingToAnalyzeSettlesAStartingPointAsNothing(t *testing.T) {
	h, _ := remoteHarness(t)
	h.fetchErr["octo/empty"] = &NothingToAnalyze{Tree: Tree{Ref: "HEAD", SHA: "sha-empty"}, Why: "octo/empty has no workflows or action manifest"}
	h.enqueue(item("octo", "empty", "", 0))

	h.run(10)

	status, sha, _ := statusOf(t, h.store, "octo/empty")
	if status != frontier.StatusNothing || !strings.HasPrefix(sha, "sha-empty") {
		t.Errorf("octo/empty = %s at %s, want nothing at its commit", status, sha)
	}
}

// Admission is the coordinator's: a worker holds no store and cannot ask whether a repository is
// declared or licensed. A repository the source policy refuses is never handed to one, and settles
// exactly as the same refusal settles a local fetch.
func TestARepositoryTheSourcePolicyRefusesIsNeverHandedToAWorker(t *testing.T) {
	h, tasks := remoteHarness(t)
	h.expander.SourcePolicy = policyOf("private")
	h.enqueue(item("stranger", "tool", "", 1))

	h.run(10)

	if len(*tasks) != 0 || len(h.fetched) != 0 {
		t.Fatalf("tasks %+v and fetches %v for a refused repository", *tasks, h.fetched)
	}
	var status, reason string
	if err := h.store.DB().QueryRow(`SELECT status, COALESCE(reason, '') FROM abom_frontier WHERE node_key = 'stranger/tool'`).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != frontier.StatusUnavailable || !strings.Contains(reason, sourcepolicy.RefusalPrefix) {
		t.Errorf("stranger/tool = %s (%q); want unavailable for the policy's reason", status, reason)
	}
}

// With no source policy there is nobody to admit anything, so nothing is sent.
func TestNothingIsHandedToAWorkerWithoutASourcePolicy(t *testing.T) {
	h, tasks := remoteHarness(t)
	h.expander.SourcePolicy = nil
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))

	h.run(10)

	if len(*tasks) != 0 {
		t.Errorf("tasks %+v sent with no source policy to admit them", *tasks)
	}
}

// A task arrives inside a grant the coordinator signed, so one that names no admission is one some
// path built without asking: the worker refuses it before fetching anything, and for good, since
// the same task asked again names no admission either.
func TestAWorkerRefusesATaskThatNamesNoAdmission(t *testing.T) {
	h := newHarness(t)
	worker := &Expander{Fetcher: h, Builder: h, CodeQL: h, Decode: h.decode}

	_, err := worker.AnalyzeTask(context.Background(), RemoteTask{Owner: "gradle", Repo: "actions"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "admission") || !IsUnavailable(err) {
		t.Fatalf("AnalyzeTask = %v; want the unadmitted task refused for good", err)
	}
	if len(h.fetched) != 0 {
		t.Errorf("fetched %v for an unadmitted task", h.fetched)
	}
}

// lanesHarness drains a two-lane suite through workers, then changes the findings lane, so the next
// pass has the graph lane on record and the findings lane outstanding. answer stands in for the
// worker on that next pass.
func lanesHarness(t *testing.T, answer func(*Expander, context.Context, RemoteTask, string) (RemoteOutput, error)) (*harness, *[]RemoteTask) {
	t.Helper()
	h, a := findingsHarness(t)
	h.expander.SourcePolicy = policyOf("public")
	worker := &Expander{Fetcher: h, Builder: h, CodeQL: h, Analyzer: a, Decode: h.decode}
	var tasks []RemoteTask
	drained := false
	h.expander.Remote = remoteFunc(func(ctx context.Context, task RemoteTask, dir string) (RemoteOutput, error) {
		if !drained {
			return worker.AnalyzeTask(ctx, task, dir)
		}
		tasks = append(tasks, task)
		return answer(worker, ctx, task, dir)
	})
	h.enqueue(item("actions", "checkout", "", 1))
	for range 10 {
		if h.pending() == 0 {
			break
		}
		runBound(t, h)
	}
	drained = true
	bindLanes(t, h, "abom/edges", "Injection.ql")
	h.requeueDirect()
	return h, &tasks
}

// A worker runs only the lanes the ledger lacks at the commit, as a database built here would: a
// findings change sends the findings lane, and the graph lane the ledger holds stays home.
func TestAWorkerRunsOnlyTheLanesTheLedgerLacks(t *testing.T) {
	h, tasks := lanesHarness(t, func(w *Expander, ctx context.Context, task RemoteTask, dir string) (RemoteOutput, error) {
		return w.AnalyzeTask(ctx, task, dir)
	})
	runBound(t, h)

	if len(*tasks) == 0 {
		t.Fatal("nothing was sent for the lane that changed")
	}
	for _, task := range *tasks {
		if len(task.Queries) != 0 || len(task.Findings) == 0 {
			t.Errorf("%s/%s was sent %d graph queries and %d findings queries; want the findings lane only",
				task.Owner, task.Repo, len(task.Queries), len(task.Findings))
		}
	}
}

// A worker fetches by reference, so the commit can move after the lanes were chosen. A move to the
// same content needs nothing more; one to new content needs the graph lane the worker was not sent,
// and is tried again and planned anew rather than settled short.
func TestACommitThatMovedUnderAWorkerIsPlannedAgain(t *testing.T) {
	h, tasks := lanesHarness(t, func(w *Expander, ctx context.Context, task RemoteTask, dir string) (RemoteOutput, error) {
		out, err := w.AnalyzeTask(ctx, task, dir)
		out.Commit, out.Digest = "sha-moved", "content-changed"
		return out, err
	})
	stats := runBound(t, h)

	if len(*tasks) == 0 || stats.Failed != len(*tasks) {
		t.Errorf("stats %+v for %d tasks; want each one whose commit moved failed, to be retried", stats, len(*tasks))
	}
	var analyzed int
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_run WHERE db_commit = 'sha-moved'`).Scan(&analyzed); err != nil {
		t.Fatal(err)
	}
	if analyzed != 0 {
		t.Errorf("%d runs recorded at the moved commit, which was never analyzed in full", analyzed)
	}
}
