package expand

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// remoteRun has the run pool's workers take every member a run holds no database for, answering
// as answer says; the crawl's own stays local, so a member reaching it would be built here.
func remoteRun(t *testing.T, answer func(RemoteTask, string) (RemoteOutput, error)) (*harness, *Executor, int64, *fakeAnalyzer, *[]RemoteTask) {
	t.Helper()
	h, e, id, analyzer := executorHarness(t)
	delete(h.fetchErr, "octo/absent")
	h.expander.SourcePolicy = policyOf("public")
	var tasks []RemoteTask
	e.Remote = remoteFunc(func(_ context.Context, task RemoteTask, dir string) (RemoteOutput, error) {
		tasks = append(tasks, task)
		return answer(task, dir)
	})
	return h, e, id, analyzer, &tasks
}

// findingsAt is a worker that analyzed the member at commit and found what the fixture holds.
func findingsAt(commit string) func(RemoteTask, string) (RemoteOutput, error) {
	return func(_ RemoteTask, dir string) (RemoteOutput, error) {
		body, err := os.ReadFile(fixtureFindings)
		if err != nil {
			return RemoteOutput{}, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return RemoteOutput{}, err
		}
		out := RemoteOutput{Commit: commit, SarifPath: filepath.Join(dir, "findings.sarif")}
		return out, os.WriteFile(out.SarifPath, body, 0o644)
	}
}

// A member with no database here is fetched, built and analyzed by a worker at the commit on
// record, and what it found is recorded exactly as a member analyzed here: nothing is built here.
func TestARunMemberWithNoDatabaseHereIsAnalyzedByAWorker(t *testing.T) {
	h, e, id, analyzer, tasks := remoteRun(t, findingsAt(absentCommit))

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(*tasks) != 1 {
		t.Fatalf("sent %d tasks; want one, for the member with no database", len(*tasks))
	}
	task := (*tasks)[0]
	if task.Owner+"/"+task.Repo != "octo/absent" || task.Ref != absentCommit || task.Admitted == "" ||
		len(task.Findings) == 0 || len(task.Queries) != 0 {
		t.Errorf("task = %+v; want octo/absent at its commit, admitted, with the run's findings queries only", task)
	}
	absent := membersByRepository(t, h.store, id)["octo/absent"]
	if absent.Status != store.AnalysisSucceeded || !absent.Counted || absent.Results != 2 || absent.Commit != absentCommit {
		t.Errorf("the member is %q counted=%v results=%d at %q; want the 2 the worker's SARIF reported at %s",
			absent.Status, absent.Counted, absent.Results, absent.Commit, absentCommit)
	}
	if absent.SourceFilter != h.expander.sourceFilter() {
		t.Errorf("the member names filter %q, want %q", absent.SourceFilter, h.expander.sourceFilter())
	}
	if built := h.databasesBuilt(); built != 0 {
		t.Errorf("built %d databases here; the worker builds them", built)
	}
	if len(analyzer.targets) != 2 || slices.ContainsFunc(analyzer.targets, func(t []string) bool { return len(t) == 0 }) {
		t.Errorf("analyzed %d here; want only the two members whose databases are held here", len(analyzer.targets))
	}
	var commit string
	if err := h.store.DB().QueryRow(`SELECT db_commit FROM abom_query_run WHERE repository = 'octo/absent'`).
		Scan(&commit); err != nil || commit != absentCommit {
		t.Errorf("the evaluation is at %q (%v); want the commit the worker analyzed", commit, err)
	}
}

// What a worker brought back is recorded and then removed, as a member built here is: a run leaves
// no member's scratch behind it.
func TestARunMemberAWorkerAnalyzedLeavesNothingBehind(t *testing.T) {
	var scratch string
	answer := findingsAt(absentCommit)
	_, e, id, _, _ := remoteRun(t, func(task RemoteTask, dir string) (RemoteOutput, error) {
		scratch = dir
		if err := os.MkdirAll(filepath.Join(dir, "lock"), 0o755); err != nil {
			return RemoteOutput{}, err
		}
		if err := os.WriteFile(filepath.Join(dir, "lock", "actions.lock"), []byte("lockVersion: 0.0.3\n"), 0o644); err != nil {
			return RemoteOutput{}, err
		}
		return answer(task, dir)
	})

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(scratch); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s is still there after the run recorded it: %v", scratch, err)
	}
}

// What a worker found nothing to analyze in is nothing, not a failure, as it is here.
func TestARunMemberAWorkerFoundNothingInIsNotAFailure(t *testing.T) {
	h, e, id, _, _ := remoteRun(t, func(RemoteTask, string) (RemoteOutput, error) {
		return RemoteOutput{}, &NothingToAnalyze{Tree: Tree{SHA: absentCommit}, Why: "octo/absent has no workflows or action manifest"}
	})

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if absent := membersByRepository(t, h.store, id)["octo/absent"]; absent.Status != store.AnalysisNothing {
		t.Errorf("the member is %q (%s); want nothing to analyze", absent.Status, absent.Detail)
	}
}

// A member the source policy refuses is never handed to a worker, and says why.
func TestARunMemberTheSourcePolicyRefusesIsNeverHandedToAWorker(t *testing.T) {
	h, e, id, _, tasks := remoteRun(t, findingsAt(absentCommit))
	h.expander.SourcePolicy = policyOf("private")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*tasks) != 0 {
		t.Errorf("sent %+v for a refused repository", *tasks)
	}
	if absent := membersByRepository(t, h.store, id)["octo/absent"]; absent.Status != store.AnalysisFailed ||
		!strings.Contains(absent.Detail, sourcepolicy.RefusalPrefix) {
		t.Errorf("the member is %q (%s); want it failed for the policy's reason", absent.Status, absent.Detail)
	}
}
