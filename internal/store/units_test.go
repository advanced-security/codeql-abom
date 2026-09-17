package store

import "testing"

// A unit only exists once its columns are its own. The dependencies unit rendered five kinds
// through one column set, so 1,138 of 1,585 rows on the crawled organization read zero for reach,
// total refs and distinct refs at once — 1,107 of them workflows, which nothing depends on because
// a workflow is a consumer. These pin the two units that carry the kinds out of it.

func TestWorkflowsPageListsOnlyWorkflowKinds(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 2)

	page, err := s.WorkflowsPage(WorkflowsQuery{})
	if err != nil {
		t.Fatalf("WorkflowsPage: %v", err)
	}
	if page.Total == 0 {
		t.Fatal("matched nothing, so this proves nothing")
	}
	for _, w := range page.Items {
		if w.Kind != "workflow" && w.Kind != "reusable_workflow" {
			t.Errorf("row %q is a %s, which belongs to another unit", w.NodeKey, w.Kind)
		}
	}
	if !hasKey(page.Items, "octo-org/app/.github/workflows/release.yml") {
		t.Error("the workflow the fixture declared is not listed")
	}
}

func hasKey(items []WorkflowSummary, key string) bool {
	for _, w := range items {
		if w.NodeKey == key {
			return true
		}
	}
	return false
}

// The columns that made a workflow read as an empty row are gone; these are the ones it carries.
func TestWorkflowsPageCountsItsOwnJobsStepsAndDependencies(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 2)

	page, err := s.WorkflowsPage(WorkflowsQuery{})
	if err != nil {
		t.Fatalf("WorkflowsPage: %v", err)
	}
	w := findWorkflow(t, page.Items, "octo-org/app/.github/workflows/release.yml")
	if w.Jobs != 1 {
		t.Errorf("counted %d jobs, want 1", w.Jobs)
	}
	if w.Steps != 2 {
		t.Errorf("counted %d steps, want 2", w.Steps)
	}
	if w.Dependencies != 2 {
		t.Errorf("counted %d dependencies, want the 2 its steps use", w.Dependencies)
	}
	if w.Repository != "octo-org/app" {
		t.Errorf("named repository %q, want octo-org/app", w.Repository)
	}
}

// `on:` is workflow-level, so every job of a workflow carries the same triggers and reading one is
// reading the workflow's. Privileged is the opposite: it is per job, and the workflow's answer is
// whether *any* of them is, which is the question a reader is actually asking.
func TestWorkflowsPageReadsTriggersFromItsJobsAndPrivilegeFromAny(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 1)
	observe(t, s, "job:octo-org/app:.github/workflows/release.yml:build", "triggers", "pull_request")
	observe(t, s, "job:octo-org/app:.github/workflows/release.yml:build", "privileged", "true")

	page, err := s.WorkflowsPage(WorkflowsQuery{})
	if err != nil {
		t.Fatalf("WorkflowsPage: %v", err)
	}
	w := findWorkflow(t, page.Items, "octo-org/app/.github/workflows/release.yml")
	if got := w.Triggers; got != "pull_request" {
		t.Errorf("read triggers %q, want pull_request", got)
	}
	if w.PrivilegedJobs != 1 {
		t.Errorf("counted %d privileged jobs, want 1", w.PrivilegedJobs)
	}
}

func TestJobsPageListsJobsWithTheirOwnFacts(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 2)
	observe(t, s, "job:octo-org/app:.github/workflows/release.yml:build", "runsOn", "ubuntu-latest")
	observe(t, s, "job:octo-org/app:.github/workflows/release.yml:build", "privileged", "true")
	observe(t, s, "job:octo-org/app:.github/workflows/release.yml:build", "permissionsScope", "job")

	page, err := s.JobsPage(JobsQuery{})
	if err != nil {
		t.Fatalf("JobsPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 job", page.Total)
	}
	j := page.Items[0]
	if j.Workflow != "octo-org/app/.github/workflows/release.yml" {
		t.Errorf("named workflow %q", j.Workflow)
	}
	if j.RunsOn != "ubuntu-latest" {
		t.Errorf("read runs-on %q, want ubuntu-latest", j.RunsOn)
	}
	if !j.Privileged {
		t.Error("read the job as unprivileged, want privileged")
	}
	if j.PermissionsScope != "job" {
		t.Errorf("read permissions scope %q, want job", j.PermissionsScope)
	}
	if j.Steps != 2 {
		t.Errorf("counted %d steps, want 2", j.Steps)
	}
	if j.Dependencies != 2 {
		t.Errorf("counted %d dependencies, want 2", j.Dependencies)
	}
}

// A job's steps are joined by `job_id` within the declaring file, never by the container link,
// which records the file rather than the parent job. Two jobs in one workflow must not pool steps.
func TestJobsPageDoesNotPoolTheStepsOfItsSiblings(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "test", 1)

	page, err := s.JobsPage(JobsQuery{Sort: "jobId"})
	if err != nil {
		t.Fatalf("JobsPage: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("matched %d jobs, want 2", page.Total)
	}
	for _, j := range page.Items {
		want := map[string]int{"build": 2, "test": 1}[j.JobID]
		if j.Steps != want {
			t.Errorf("job %q counted %d steps, want %d", j.JobID, j.Steps, want)
		}
	}
}

// The actions unit keeps the columns that were always its own, and stops claiming the kinds that
// cannot answer them.
func TestActionsInventoryNoLongerListsWorkflows(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 1)

	page, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	for _, it := range page.Items {
		if it.Kind == "workflow" || it.Kind == "reusable_workflow" {
			t.Errorf("row %q is a %s, which has its own unit", it.NodeKey, it.Kind)
		}
	}
}

// Naming a kind still reaches past the default, or a scope built elsewhere stops working here.
func TestActionsInventoryStillReachesAKindByName(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 1)

	page, err := s.ActionsInventoryPage(InventoryQuery{Kinds: []string{"workflow"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total == 0 {
		t.Error("asking for workflows by name matched nothing; the default is a cage")
	}
	for _, it := range page.Items {
		if it.Kind != "workflow" {
			t.Errorf("row %q is a %s, which was not asked for", it.NodeKey, it.Kind)
		}
	}
}

func findWorkflow(t *testing.T, items []WorkflowSummary, key string) WorkflowSummary {
	t.Helper()
	for _, w := range items {
		if w.NodeKey == key {
			return w
		}
	}
	t.Fatalf("%q is not in the page", key)
	return WorkflowSummary{}
}
