package store

import (
	"reflect"
	"testing"
)

func TestLiveCommitEvidenceReportsAnalyzedEquivalentAndOutstandingCommits(t *testing.T) {
	s := newTestStore(t)
	run := observeAt(t, s, "c1", sharedLocation, "actions/cache", "v4", "")
	if err := s.MarkSightedContent(run); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []string{"c1", "c2", "c3"} {
		standAt(t, s, commit, commit)
	}
	for _, commit := range []string{"c1", "c2"} {
		if _, err := s.db.Exec(`INSERT INTO abom_commit_content(repository, commit_sha, indexed_digest, seen_at)
			VALUES ('vendor/comp', ?, 'same', ?)`, commit, now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE abom_frontier SET status = 'pending' WHERE resolved_sha = 'c3'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LiveCommitEvidence()
	want := []LiveCommitEvidence{
		{Commit: "vendor/comp@c1", Answers: 1, Content: true, Analyzed: 1},
		{Commit: "vendor/comp@c2", Answers: 1, Content: true, Analyzed: 1},
		{Commit: "vendor/comp@c3", Answers: 0, Content: false, Analyzed: 1, Outstanding: true},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("live commit evidence=%+v error=%v, want %+v", got, err, want)
	}
}
