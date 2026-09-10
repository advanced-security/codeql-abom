package store

import (
	"testing"
)

// The closure is where a submitted dependency gets its version, so the resolution has to reach it.
// A commit-pinned entry carries the version only when something measured it: a trailing comment is
// what somebody intended, and submitting on that basis names a dependency nothing confirmed.
func TestSubmissionClosureCarriesAMeasuredVersionForACommitPin(t *testing.T) {
	s := newTestStore(t)
	sha := "de0fac2e4500dabe0009e67214ff5f5447ce83dd"
	closureFixtureWithRef(t, s, "octo-org/app", "actions", "checkout", sha)
	if err := s.RecordRefResolution(RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2", CommitSHA: sha,
		Source: ResolutionCrawl,
	}); err != nil {
		t.Fatalf("RecordRefResolution: %v", err)
	}

	got := closureFor(t, s, "octo-org/app")
	if len(got) == 0 {
		t.Fatal("the closure is empty, so nothing about versions can be asserted")
	}
	if got[0].Version != "v4.2.2" {
		t.Errorf("Version = %q, want v4.2.2", got[0].Version)
	}
	if got[0].Ref != sha {
		t.Errorf("Ref = %q, want the commit to survive alongside the version", got[0].Ref)
	}
}

func TestSubmissionClosureCarriesNoVersionForAClaim(t *testing.T) {
	s := newTestStore(t)
	sha := "de0fac2e4500dabe0009e67214ff5f5447ce83dd"
	closureFixtureWithRef(t, s, "octo-org/app", "actions", "checkout", sha)
	if err := s.RecordRefResolution(RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2", CommitSHA: sha,
		Source: ResolutionComment,
	}); err != nil {
		t.Fatalf("RecordRefResolution: %v", err)
	}

	got := closureFor(t, s, "octo-org/app")
	if len(got) == 0 {
		t.Fatal("the closure is empty, so nothing about versions can be asserted")
	}
	if got[0].Version != "" {
		t.Errorf("Version = %q on a comment alone; a claim must not reach a submission", got[0].Version)
	}
}

func closureFor(t *testing.T, s *Store, repository string) []ClosureEntry {
	t.Helper()
	got, err := s.SubmissionClosure(repository)
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	return got
}

// The smallest shape the closure reads: one seed repository whose workflow names one external
// action at the given reference.
func closureFixtureWithRef(t *testing.T, s *Store, repository, owner, repo, ref string) {
	t.Helper()
	var repoID int64
	if err := s.DB().QueryRow(
		`INSERT INTO repository (repository_full_name, origin) VALUES (?, 'seed')
		 RETURNING row_id`, repository).Scan(&repoID); err != nil {
		t.Fatalf("seeding %s: %v", repository, err)
	}
	var step, action int64
	if err := s.DB().QueryRow(
		`INSERT INTO abom_node (node_key, node_kind, is_external)
		 VALUES (?, 'step', 0) RETURNING row_id`,
		"step:"+repository+":.github/workflows/ci.yml:build:0").Scan(&step); err != nil {
		t.Fatalf("inserting the step: %v", err)
	}
	if err := s.DB().QueryRow(
		`INSERT INTO abom_node (node_key, node_kind, owner, repo, path, is_external)
		 VALUES (?, 'action', ?, ?, '', 1) RETURNING row_id`,
		owner+"/"+repo, owner, repo).Scan(&action); err != nil {
		t.Fatalf("inserting the action: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			ref_value, file_path, repository_row_id, start_line)
		VALUES ('e1', ?, ?, 'step-uses-action', ?, '.github/workflows/ci.yml', ?, 1)`,
		step, action, ref, repoID); err != nil {
		t.Fatalf("inserting the edge: %v", err)
	}
}
