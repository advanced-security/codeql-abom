package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// protectedFixture is acme/service, owned and building from main, analyzed there at m1, where
// ci.yml names actions/checkout@v4.
func protectedFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")
	if err := s.SetDefaultBranch("acme/service", "main"); err != nil {
		t.Fatalf("SetDefaultBranch: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha)
		VALUES ('acme/service', '', 'acme', 'service', 0, 'done', ?, 'm1')`, now()); err != nil {
		t.Fatalf("queueing the default branch: %v", err)
	}
	declaredAt(t, s, "acme/service", "m1", ".github/workflows/ci.yml", 10, "actions/checkout", "v4")
	return s
}

// declaredAt records an edges run of one commit of a repository, naming dst@ref at file:line.
func declaredAt(t *testing.T, s *Store, repository, commit, file string, line int, dst, ref string) {
	t.Helper()
	var repoID int64
	if err := s.db.QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = ?`,
		repository).Scan(&repoID); err != nil {
		t.Fatalf("resolving %s: %v", repository, err)
	}
	run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: repository, DBCommit: commit})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if err := s.MarkSightedContent(run); err != nil {
		t.Fatalf("MarkSightedContent: %v", err)
	}
	workflow := repository + "/" + file
	step := fmt.Sprintf("%s:step:%d", workflow, line)
	owner, name, _ := strings.Cut(dst, "/")
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: workflow, Kind: "workflow", RepositoryID: repoID, Path: file},
		{Key: step, Kind: "step", RepositoryID: repoID, ContainerKey: workflow},
		{Key: dst, Kind: "action", Owner: owner, Repo: name, IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{Key: fmt.Sprintf("%s:%s:%d:9", repository, file, line),
		SrcKey: step, DstKey: dst, Kind: EdgeKindStepUsesAction, RefRaw: dst + "@" + ref,
		RefValue: ref, RepositoryID: repoID, QueryRunID: run, FilePath: file,
		StartLine: line, StartColumn: 9}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// analyzedAt is the crawl finishing a queue row at a commit. Fatal when the row does not exist,
// because a branch the queue never received is exactly the failure these tests look for.
func analyzedAt(t *testing.T, s *Store, nodeKey, ref, commit string) {
	t.Helper()
	res, err := s.db.Exec(`UPDATE abom_frontier SET status = 'done', resolved_sha = ?,
		analyzed_at = ? WHERE node_key = ? AND ref = ?`, commit, now(), nodeKey, ref)
	if err != nil {
		t.Fatalf("completing %s@%s: %v", nodeKey, ref, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("%s@%s has no queue row to analyze", nodeKey, ref)
	}
}

// queueRows is every row the queue holds for one node key, as ref -> depth.
func queueRows(t *testing.T, s *Store, nodeKey string) map[string]int {
	t.Helper()
	rows, err := s.db.Query(`SELECT ref, depth FROM abom_frontier WHERE node_key = ?`, nodeKey)
	if err != nil {
		t.Fatalf("reading the queue: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ref string
		var depth int
		if err := rows.Scan(&ref, &depth); err != nil {
			t.Fatal(err)
		}
		out[ref] = depth
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// walkedTargets names what the walk reads from a repository's declarations, by the commit it
// stood at.
func walkedTargets(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	live, starting, err := s.liveAndStarting()
	if err != nil {
		t.Fatalf("reading live and starting commits: %v", err)
	}
	declared, err := s.EdgeDeclarations(live, starting)
	if err != nil {
		t.Fatalf("EdgeDeclarations: %v", err)
	}
	out := map[string][]string{}
	for _, d := range declared {
		var key string
		if err := s.db.QueryRow(`SELECT node_key FROM abom_node WHERE row_id = ?`,
			d.Dst).Scan(&key); err != nil {
			t.Fatal(err)
		}
		out[key] = append(out[key], d.Commits...)
	}
	return out
}

func protect(t *testing.T, s *Store, repository string, branches ...string) BranchQueue {
	t.Helper()
	got, err := s.SetProtectedBranches(repository, branches)
	if err != nil {
		t.Fatalf("SetProtectedBranches(%v): %v", branches, err)
	}
	return got
}

// A protected branch is where workflows run with secrets and where pull_request_target and
// schedule read from, so what it declares is what the repository runs as much as main is.
func TestAProtectedBranchsCommitIsLiveAndItsDeclarationsAreWalked(t *testing.T) {
	s := protectedFixture(t)

	protect(t, s, "acme/service", "main", "release")
	analyzedAt(t, s, "acme/service", "release", "r1")
	declaredAt(t, s, "acme/service", "r1", ".github/workflows/release.yml", 5, "actions/cache", "v3")

	live, err := s.LiveCommits()
	if err != nil {
		t.Fatalf("LiveCommits: %v", err)
	}
	if _, ok := live["acme/service"]["r1"]; !ok {
		t.Errorf("live commits of acme/service are %v, want release's r1 among them",
			live["acme/service"])
	}
	walked := walkedTargets(t, s)
	if !slices.Contains(walked["actions/cache"], "r1") {
		t.Errorf("the walk read actions/cache at %v, want it read from release's r1",
			walked["actions/cache"])
	}
	if !slices.Contains(walked["actions/checkout"], "m1") {
		t.Errorf("the walk read actions/checkout at %v, want main's m1 still read",
			walked["actions/checkout"])
	}
}

// A branch that loses protection is no longer where anything runs with secrets. Its commit is
// history from then on, and a walk that still stood there would report a dependency nobody runs.
func TestABranchThatStopsBeingProtectedLeavesEveryWalk(t *testing.T) {
	s := protectedFixture(t)
	protect(t, s, "acme/service", "main", "release")
	analyzedAt(t, s, "acme/service", "release", "r1")
	declaredAt(t, s, "acme/service", "r1", ".github/workflows/release.yml", 5, "actions/cache", "v3")

	got := protect(t, s, "acme/service", "main")

	if got.Removed != 1 {
		t.Errorf("Removed = %d, want the release row", got.Removed)
	}
	if rows := queueRows(t, s, "acme/service"); len(rows) != 1 || rows[""] != 0 {
		t.Errorf("acme/service is queued as %v, want only its default branch", rows)
	}
	live, err := s.LiveCommits()
	if err != nil {
		t.Fatalf("LiveCommits: %v", err)
	}
	if _, ok := live["acme/service"]["r1"]; ok {
		t.Errorf("r1 is still live after release lost protection: %v", live["acme/service"])
	}
	walked := walkedTargets(t, s)
	if len(walked["actions/cache"]) > 0 {
		t.Errorf("the walk still reads actions/cache at %v, which only the unprotected branch declared",
			walked["actions/cache"])
	}
	if !slices.Contains(walked["actions/checkout"], "m1") {
		t.Errorf("the walk read actions/checkout at %v, want main's m1 untouched",
			walked["actions/checkout"])
	}
}

// The default branch is already the repository's empty-ref row. A second row for it would resolve
// to the same commit and analyze it twice.
func TestTheDefaultBranchIsNotQueuedTwice(t *testing.T) {
	s := protectedFixture(t)

	got := protect(t, s, "acme/service", "main", "release")

	rows := queueRows(t, s, "acme/service")
	if _, dup := rows["main"]; dup {
		t.Errorf("the default branch was queued a second time: %v", rows)
	}
	if depth, ok := rows["release"]; !ok || depth != 0 {
		t.Errorf("acme/service is queued as %v, want release at depth 0 beside the default", rows)
	}
	if got.Queued != 1 {
		t.Errorf("Queued = %d, want 1", got.Queued)
	}
}

// Without the default branch on record, any protected branch may be the one the empty-ref row
// already analyzes. Queueing all of them would analyze that commit twice, so none is queued until
// it is known which is which.
func TestNoBranchIsQueuedWhileTheDefaultBranchIsUnknown(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")
	enqueueRoot(t, s, "acme/service", "done")

	got := protect(t, s, "acme/service", "main", "release")

	if rows := queueRows(t, s, "acme/service"); len(rows) != 1 {
		t.Errorf("acme/service is queued as %v with no default branch on record, want only ''", rows)
	}
	if got.Queued != 0 {
		t.Errorf("Queued = %d, want 0", got.Queued)
	}
	if branches, asked, _ := s.ProtectedBranches("acme/service"); !asked || len(branches) != 2 {
		t.Errorf("ProtectedBranches = %v (asked=%v), want the answer recorded anyway", branches, asked)
	}
}

// Recording the same answer is what every enumeration does. A row analyzed at its branch must keep
// its commit and its status, or every pass would re-analyze every protected branch.
func TestRecordingTheSameAnswerAgainKeepsWhatWasAnalyzed(t *testing.T) {
	s := protectedFixture(t)
	protect(t, s, "acme/service", "main", "release")
	analyzedAt(t, s, "acme/service", "release", "r1")

	got := protect(t, s, "acme/service", "main", "release")

	var status, sha string
	if err := s.db.QueryRow(`SELECT status, COALESCE(resolved_sha, '') FROM abom_frontier
		WHERE node_key = 'acme/service' AND ref = 'release'`).Scan(&status, &sha); err != nil {
		t.Fatalf("reading release: %v", err)
	}
	if status != "done" || sha != "r1" {
		t.Errorf("release is %s at %q, want done at r1", status, sha)
	}
	if got.Queued != 0 || got.Removed != 0 {
		t.Errorf("an unchanged answer reported %+v, want nothing queued or removed", got)
	}
}

// Only what the operator owns lives at its protected branches. A repository outside the
// declaration keeps the recorded answer and gains no depth-0 row, which would make it the
// operator's own work.
func TestARepositoryOutsideTheDeclarationQueuesNoBranch(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")
	if _, err := s.UpsertRepository("stranger/app", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.SetDefaultBranch("stranger/app", "main"); err != nil {
		t.Fatalf("SetDefaultBranch: %v", err)
	}

	got := protect(t, s, "stranger/app", "main", "release")

	if rows := queueRows(t, s, "stranger/app"); len(rows) != 0 {
		t.Errorf("stranger/app is queued as %v, want nothing", rows)
	}
	if got.Queued != 0 {
		t.Errorf("Queued = %d, want 0", got.Queued)
	}
}

// A protected branch can be the same queue row as a dependency: another repository naming
// acme/service@release. Losing protection ends the first reason and not the second, so the row
// stays, at the depth its referrer gives it, and its commit stays live through the reference.
func TestUnprotectingABranchADependencyStillNamesKeepsItQueued(t *testing.T) {
	s := protectedFixture(t)
	seedOwnedRepo(t, s, "acme/app")
	enqueueRoot(t, s, "acme/app", "done")
	declaredAt(t, s, "acme/app", "a1", ".github/workflows/ci.yml", 3, "acme/service", "release")
	protect(t, s, "acme/service", "main", "release")

	got := protect(t, s, "acme/service", "main")

	if depth, ok := queueRows(t, s, "acme/service")["release"]; !ok || depth != 1 {
		t.Errorf("acme/service@release is queued at depth %d (present=%v), want depth 1: "+
			"acme/app still names it", depth, ok)
	}
	if got.Removed != 0 {
		t.Errorf("Removed = %d, want 0 while a dependency still names the row", got.Removed)
	}
}

// A repository is one repository however many branches it is live at. Counting each branch as a
// repository would report more repositories than the operator owns.
func TestAProtectedBranchDoesNotCountItsRepositoryTwice(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "acme")
	enqueueRoot(t, s, "acme/service", "done")
	enqueueRef(t, s, "acme/service", "release", 0, "pending")
	enqueueRef(t, s, "acme/service", "hotfix", 0, "done")
	enqueueRoot(t, s, "acme/waiting", "pending")
	enqueueDependency(t, s, "acme/action", "v1", "done")

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	owned := counts[TierOwned]

	if owned.Repositories != 2 || owned.RepositoriesAnalyzed != 1 {
		t.Errorf("repositories = %d of %d analyzed, want 1 of 2",
			owned.RepositoriesAnalyzed, owned.Repositories)
	}
	if owned.Branches != 2 || owned.BranchesAnalyzed != 1 {
		t.Errorf("protected branches = %d of %d analyzed, want 1 of 2",
			owned.BranchesAnalyzed, owned.Branches)
	}
	if owned.Dependencies != 1 || owned.DependenciesAnalyzed != 1 {
		t.Errorf("dependencies = %d of %d analyzed, want 1 of 1",
			owned.DependenciesAnalyzed, owned.Dependencies)
	}
	if owned.Repositories+owned.Branches+owned.Dependencies != owned.Total {
		t.Errorf("%d repositories + %d branches + %d dependencies != %d rows",
			owned.Repositories, owned.Branches, owned.Dependencies, owned.Total)
	}
	if owned.RepositoriesAnalyzed+owned.BranchesAnalyzed+owned.DependenciesAnalyzed != owned.Analyzed {
		t.Errorf("%d + %d + %d analyzed != %d", owned.RepositoriesAnalyzed,
			owned.BranchesAnalyzed, owned.DependenciesAnalyzed, owned.Analyzed)
	}
}

// A unit and a tier answer "how much of this has been analyzed" from the same rows, so a
// repository analyzed only at a protected branch is unanalyzed in both: its default branch, the
// one everything else reads, has not been looked at.
func TestAUnitCountsARepositoryAsAnalyzedByItsDefaultBranch(t *testing.T) {
	s := unitCoverageFixture(t)
	enqueueRef(t, s, "octo-org/pending", "release", 0, "done")
	enqueueRef(t, s, "octo-org/paid", "release", 0, "pending")

	breakdown, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatalf("PropertyBreakdown: %v", err)
	}
	for _, v := range breakdown.Values {
		if v.Value == "3" && (v.Repositories != 2 || v.Analyzed != 1) {
			t.Errorf("unit 3 reports %d of %d analyzed, want 1 of 2", v.Analyzed, v.Repositories)
		}
	}
	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if owned := counts[TierOwned]; owned.RepositoriesAnalyzed != 2 {
		t.Errorf("the tier reports %d repositories analyzed, want the 2 the units add up to",
			owned.RepositoriesAnalyzed)
	}
}

// A snapshot describes the default branch: GitHub files it under refs/heads/<default> at that
// branch's head. What only a protected branch declares is not a dependency of that commit, and
// submitting it would put a dependency the default branch does not have into GitHub's graph.
func TestASnapshotLeavesOutWhatOnlyAProtectedBranchDeclares(t *testing.T) {
	s := protectedFixture(t)
	enqueueRef(t, s, "acme/service", "release", 0, "pending")
	analyzedAt(t, s, "acme/service", "release", "r1")
	declaredAt(t, s, "acme/service", "r1", ".github/workflows/release.yml", 5, "actions/cache", "v3")

	closure, err := s.SubmissionClosure("acme/service")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	var named []string
	for _, e := range closure {
		named = append(named, e.Owner+"/"+e.Repo)
	}
	if slices.Contains(named, "actions/cache") {
		t.Errorf("the default branch's closure names %v, including actions/cache only release declares",
			named)
	}
	if !slices.Contains(named, "actions/checkout") {
		t.Errorf("the default branch's closure names %v, want actions/checkout main declares", named)
	}
}

// With the default branch not yet analyzed, nothing is known about what it declares. Reading a
// protected branch in its place would submit that branch's dependencies as the default's.
func TestASnapshotNeverStandsAtAProtectedBranchInPlaceOfTheDefault(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")
	enqueueRoot(t, s, "acme/service", "pending")
	enqueueRef(t, s, "acme/service", "release", 0, "pending")
	analyzedAt(t, s, "acme/service", "release", "r1")
	declaredAt(t, s, "acme/service", "r1", ".github/workflows/release.yml", 5, "actions/cache", "v3")

	closure, err := s.SubmissionClosure("acme/service")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	if len(closure) != 0 {
		t.Errorf("the closure of an unanalyzed default branch is %+v, want nothing", closure)
	}
}
