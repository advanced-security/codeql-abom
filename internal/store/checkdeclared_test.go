package store

import (
	"reflect"
	"testing"
)

func standAt(t *testing.T, s *Store, ref, commit string) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
		enqueued_at, resolved_sha) VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done',
		'2026-10-01T00:00:00Z', ?)`, ref, commit); err != nil {
		t.Fatal(err)
	}
}

// Check 1 asks what the graph says each commit it stands at declared, so the population is read
// the way a walk reads it: a commit settled as identical to an analyzed one answers with that one's
// sightings, and one whose runs kept no content is unknown rather than a claim of anything.
func TestEveryCommitTheQueueStandsAtIsReadWithWhatAnswersForIt(t *testing.T) {
	s := newTestStore(t)
	for _, c := range []struct{ commit, ref string }{{"c1", "v3"}, {"c2", "v4"}} {
		run := observeAt(t, s, c.commit, sharedLocation, "actions/cache", c.ref, "")
		if err := s.MarkSightedContent(run); err != nil {
			t.Fatal(err)
		}
		standAt(t, s, c.ref, c.commit)
	}
	standAt(t, s, "v5", "c3")
	for _, commit := range []string{"c2", "c3"} {
		if _, err := s.DB().Exec(`INSERT INTO abom_commit_content (repository, commit_sha,
			indexed_digest, seen_at) VALUES ('vendor/comp', ?, 'same', '2026-10-01T00:00:00Z')`,
			commit); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.DeclaredCommits()
	if err != nil {
		t.Fatal(err)
	}
	answers := map[string][]string{}
	for _, d := range got {
		if d.Repository != "vendor/comp" || d.Unknown || d.Owned || d.Changed == "" {
			t.Errorf("%+v: want vendor/comp, known, not owned, with when it was analyzed", d)
		}
		answers[d.Commit] = d.Answers
	}
	want := map[string][]string{"c1": {"c1"}, "c2": {"c2"}, "c3": {"c2"}}
	if !reflect.DeepEqual(answers, want) {
		t.Errorf("answers = %v, want %v", answers, want)
	}
}

// A repository analyzed at two commits before sightings kept content shares rows between them, so
// neither commit's declarations are known and neither may be compared as if they were.
func TestACommitWhoseRunsKeptNoContentIsUnknown(t *testing.T) {
	s := newTestStore(t)
	for _, c := range []struct{ commit, ref string }{{"c1", "v3"}, {"c2", "v4"}} {
		observeAt(t, s, c.commit, sharedLocation, "actions/cache", c.ref, "")
		standAt(t, s, c.ref, c.commit)
	}

	got, err := s.DeclaredCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Unknown || !got[1].Unknown {
		t.Errorf("DeclaredCommits = %+v, want both commits unknown", got)
	}
}

// One location's row holds the last writer's target, so what an earlier commit declared there is
// its own sighting, not the row: reading the row would compare a commit with another one's files.
func TestACommitDeclaredWhatItsOwnRunSighted(t *testing.T) {
	s := newTestStore(t)
	for _, c := range []struct{ commit, ref string }{{"c1", "v3"}, {"c2", "v4"}} {
		run := observeAt(t, s, c.commit, sharedLocation, "actions/cache", c.ref, "")
		if err := s.MarkSightedContent(run); err != nil {
			t.Fatal(err)
		}
	}

	for commit, ref := range map[string]string{"c1": "v3", "c2": "v4"} {
		got, err := s.CommitDeclarations("vendor/comp", []string{commit})
		if err != nil {
			t.Fatal(err)
		}
		want := []Declaration{{File: "action.yml", Line: 15, Kind: EdgeKindCompositeStepUsesAction,
			Raw: "actions/cache@" + ref, Target: "actions/cache", Ref: ref}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CommitDeclarations(%s) = %+v, want %+v", commit, got, want)
		}
	}
}
