package store

import (
	"slices"
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
	if err := s.RecordLockReading("owner/site", "c1", coveredRows(), want); err != nil {
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
	if err := s.RecordLockReading("owner/site", "c1", coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	smaller := pagesGraph()
	smaller.Dependencies = smaller.Dependencies[:1]
	smaller.Uses = nil
	smaller.WorkflowPins = smaller.WorkflowPins[:1]
	if err := s.RecordLockReading("owner/site", "c1", coveredRows(), smaller); err != nil {
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

// Two consumers can lock the same tag to different commits, and each keeps its own answer. The
// digests are two public consumers' locks of `dtolnay/rust-toolchain@v1`, measured 2026-09-27.
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
		if err := s.RecordLockReading(consumer, "c1", coveredRows(), graph); err != nil {
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

// A covered lock read before its graph was kept has coverage and no entries, which is not the
// same as a lock with no entries. Reporting it as read would leave the graph missing for good,
// because nothing fetches a commit whose reading is on record.
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
		"owner/broken":  true,
	} {
		got, err := s.HasLockCoverage(repository, "c1")
		if err != nil {
			t.Fatalf("HasLockCoverage(%s): %v", repository, err)
		}
		if got != want {
			t.Errorf("HasLockCoverage(%s) = %v, want %v", repository, got, want)
		}
	}

	if err := s.RecordLockReading("owner/covered", "c1", coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	if got, err := s.HasLockCoverage("owner/covered", "c1"); err != nil || !got {
		t.Errorf("after reading it again: HasLockCoverage = %v, %v; want true", got, err)
	}
}
