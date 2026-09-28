package store

import (
	"slices"
	"strings"
	"testing"
)

// pagesGraph is what `gh actions-lock` v0.1.6 wrote onboarding one workflow that calls a
// composite action: two pins the workflow names and one only the composite reaches.
func pagesGraph() LockGraph {
	return LockGraph{
		Dependencies: []LockDependency{
			{Pin: "actions/checkout@v4.4.0", Owner: "actions", Repo: "checkout", Ref: "v4.4.0",
				ResolvedFrom: "v4.4.0", Algorithm: "sha1",
				Digest: "11d5960a326750d5838078e36cf38b85af677262", OwnerID: 44036562, RepoID: 197814629},
			{Pin: "actions/upload-artifact@v4", Owner: "actions", Repo: "upload-artifact", Ref: "v4",
				ResolvedFrom: "v4", Algorithm: "sha1",
				Digest: "ea165f8d65b6e75b540449e92b4886f43607fa02", OwnerID: 44036562, RepoID: 192625955},
			{Pin: "actions/upload-pages-artifact@v3.0.1", Owner: "actions", Repo: "upload-pages-artifact",
				Ref: "v3.0.1", ResolvedFrom: "v3.0.1", Algorithm: "sha1",
				Digest: "56afc609e74202658d3ffba0e8f6dda462b719fa", OwnerID: 44036562, RepoID: 496012378},
		},
		Uses: []LockUse{
			{Parent: "actions/upload-pages-artifact@v3.0.1", Child: "actions/upload-artifact@v4"},
		},
		WorkflowPins: []LockWorkflowPin{
			{WorkflowPath: ".github/workflows/pages.yml", Pin: "actions/checkout@v4.4.0"},
			{WorkflowPath: ".github/workflows/pages.yml", Pin: "actions/upload-pages-artifact@v3.0.1"},
		},
	}
}

func coveredRows() []LockCoverage {
	return []LockCoverage{
		{Coverage: "covered", Schema: "v0.0.2", Digest: "aaaa"},
		{WorkflowPath: ".github/workflows/pages.yml", Coverage: "covered", Schema: "v0.0.2"},
	}
}

// The DAG is evidence the source cannot give: which commit a composite action's own `uses:`
// runs at is recorded only in the caller's lock. Keeping it next to coverage is what lets a
// nested selection be answered at all.
func TestALockReadingKeepsTheGraphTheLockDeclares(t *testing.T) {
	s := newTestStore(t)
	want := pagesGraph()
	if err := s.RecordLockReading("owner/site", "c1", testReader, coveredRows(), want); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	got, err := s.LockGraphAt("owner/site", "c1")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	if !slices.Equal(got.Dependencies, want.Dependencies) {
		t.Errorf("dependencies = %+v\nwant %+v", got.Dependencies, want.Dependencies)
	}
	if !slices.Equal(got.Uses, want.Uses) {
		t.Errorf("uses = %+v, want %+v", got.Uses, want.Uses)
	}
	if !slices.Equal(got.WorkflowPins, want.WorkflowPins) {
		t.Errorf("workflow pins = %+v, want %+v", got.WorkflowPins, want.WorkflowPins)
	}
}

// Same rule as coverage: one commit, one file, one answer. An entry the corrected reading no
// longer has must not stay behind to select a commit.
func TestReadingACommitAgainReplacesItsGraph(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordLockReading("owner/site", "c1", testReader, coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	smaller := pagesGraph()
	smaller.Dependencies = smaller.Dependencies[:1]
	smaller.Uses = nil
	smaller.WorkflowPins = smaller.WorkflowPins[:1]
	if err := s.RecordLockReading("owner/site", "c1", testReader, coveredRows(), smaller); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	got, err := s.LockGraphAt("owner/site", "c1")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	if len(got.Dependencies) != 1 || len(got.Uses) != 0 || len(got.WorkflowPins) != 1 {
		t.Errorf("after the second reading: %d dependencies, %d uses, %d workflow pins; want 1, 0, 1",
			len(got.Dependencies), len(got.Uses), len(got.WorkflowPins))
	}
}

// Two consumers can lock the same tag to different commits, and each keeps its own answer.
func TestTwoConsumersKeepTheirOwnSelectionForOneTag(t *testing.T) {
	s := newTestStore(t)
	for consumer, digest := range map[string]string{
		"owner/one": "6c977a6ca4077a0ceb28ffbe03f59d46e9ac8772",
		"owner/two": "e97e2d8cc328f1b50210efc529dca0028893a2d9",
	} {
		graph := LockGraph{Dependencies: []LockDependency{{
			Pin: "dtolnay/rust-toolchain@v1", Owner: "dtolnay", Repo: "rust-toolchain", Ref: "v1",
			ResolvedFrom: "v1", Algorithm: "sha1", Digest: digest,
		}}}
		if err := s.RecordLockReading(consumer, "c1", testReader, coveredRows(), graph); err != nil {
			t.Fatalf("RecordLockReading(%s): %v", consumer, err)
		}
	}
	for consumer, want := range map[string]string{
		"owner/one": "6c977a6ca4077a0ceb28ffbe03f59d46e9ac8772",
		"owner/two": "e97e2d8cc328f1b50210efc529dca0028893a2d9",
	} {
		got, err := s.LockGraphAt(consumer, "c1")
		if err != nil {
			t.Fatalf("LockGraphAt(%s): %v", consumer, err)
		}
		if len(got.Dependencies) != 1 || got.Dependencies[0].Digest != want {
			t.Errorf("%s selects %+v, want %s", consumer, got.Dependencies, want)
		}
	}
}

// testReader stands in for lockfile.Reader, which the store only ever compares.
const testReader = "test-parser@v1 interpretation/1"

// A covered lock read before its graph was kept has coverage and no entries, which is not the
// same as a lock with no entries. Reporting it as read would leave the graph missing for good,
// because nothing fetches a commit whose reading is on record. A reading that old names no reader
// either, so an unsupported one is read again too: a newer parser may read what it refused.
func TestACoveredLockReadBeforeItsGraphWasKeptIsReadAgain(t *testing.T) {
	s := newTestStore(t)
	for _, row := range []struct{ repository, coverage string }{
		{"owner/covered", "covered"},
		{"owner/absent", "not_onboarded"},
		{"owner/broken", "unsupported"},
	} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_lock_coverage (repository, commit_sha, workflow_path, coverage, seen_at)
			VALUES (?, 'c1', '', ?, '2026-09-20T00:00:00Z')`, row.repository, row.coverage); err != nil {
			t.Fatalf("inserting a reading from before the graph was kept: %v", err)
		}
	}
	for repository, want := range map[string]bool{
		"owner/covered": false,
		"owner/absent":  true,
		"owner/broken":  false,
	} {
		got, err := s.HasLockCoverage(repository, "c1", testReader)
		if err != nil {
			t.Fatalf("HasLockCoverage(%s): %v", repository, err)
		}
		if got != want {
			t.Errorf("HasLockCoverage(%s) = %v, want %v", repository, got, want)
		}
	}

	if err := s.RecordLockReading("owner/covered", "c1", testReader, coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	if got, err := s.HasLockCoverage("owner/covered", "c1", testReader); err != nil || !got {
		t.Errorf("after reading it again: HasLockCoverage = %v, %v; want true", got, err)
	}
}

// A reader that changed may read a lock differently: a newer parser can read a schema an older
// one refused, and a changed interpretation can refuse what an older one covered. Either way the
// recorded answer is somebody else's, so the commit is read again.
func TestALockReadByAnotherReaderIsReadAgain(t *testing.T) {
	s := newTestStore(t)
	unsupported := []LockCoverage{{Coverage: "unsupported", Schema: "v0.0.3", Reason: "newer"}}
	if err := s.RecordLockReading("owner/site", "c1", "old-parser", coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	if err := s.RecordLockReading("owner/next", "c1", "old-parser", unsupported, LockGraph{}); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	for _, repository := range []string{"owner/site", "owner/next"} {
		if got, err := s.HasLockCoverage(repository, "c1", "old-parser"); err != nil || !got {
			t.Errorf("%s under the reader that read it = %v, %v; want true", repository, got, err)
		}
		if got, err := s.HasLockCoverage(repository, "c1", testReader); err != nil || got {
			t.Errorf("%s under another reader = %v, %v; want false", repository, got, err)
		}
	}
}

// No lockfile is the one answer no reader can change: there is no file to read differently. Re-
// reading every commit of an unlocked corpus on each parser upgrade would re-fetch all of it.
func TestNoLockfileIsNotReadAgainWhenTheReaderChanges(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordLockReading("owner/plain", "c1", "old-parser",
		[]LockCoverage{{Coverage: "not_onboarded"}}, LockGraph{}); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	if got, err := s.HasLockCoverage("owner/plain", "c1", testReader); err != nil || !got {
		t.Errorf("HasLockCoverage = %v, %v; want true: nothing there could read differently", got, err)
	}
}

// What another reader read is put back in the queue so it is read again; what no reader can read
// differently, and what this reader already read, stay where they are.
func TestRequeueingPutsBackOnlyTheLocksAnotherReaderRead(t *testing.T) {
	s := newTestStore(t)
	for _, r := range []struct {
		repository, reader, coverage string
	}{
		{"owner/stale", "old-parser", "covered"},
		{"owner/refused", "old-parser", "unsupported"},
		{"owner/plain", "old-parser", "not_onboarded"},
		{"owner/current", testReader, "covered"},
	} {
		if err := s.RecordLockReading(r.repository, "c1", r.reader,
			[]LockCoverage{{Coverage: r.coverage}}, LockGraph{}); err != nil {
			t.Fatalf("RecordLockReading(%s): %v", r.repository, err)
		}
		owner, repo, _ := strings.Cut(r.repository, "/")
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref)
			VALUES (?, 'v1', ?, ?, 1, 'done', '2026-09-27T00:00:00Z', 'c1', 'v1')`,
			r.repository, owner, repo); err != nil {
			t.Fatalf("queueing %s: %v", r.repository, err)
		}
	}

	n, err := s.RequeueLocksReadByAnotherReader(testReader)
	if err != nil {
		t.Fatalf("RequeueLocksReadByAnotherReader: %v", err)
	}
	if n != 2 {
		t.Errorf("requeued %d, want 2", n)
	}
	for repository, want := range map[string]string{
		"owner/stale": "pending", "owner/refused": "pending",
		"owner/plain": "done", "owner/current": "done",
	} {
		var status string
		if err := s.DB().QueryRow(`SELECT status FROM abom_frontier WHERE node_key = ?`,
			repository).Scan(&status); err != nil {
			t.Fatalf("reading %s: %v", repository, err)
		}
		if status != want {
			t.Errorf("%s is %s, want %s", repository, status, want)
		}
	}
}
