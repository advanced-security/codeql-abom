package store

import "testing"

const (
	contentA = "digest-of-content-a"
	contentB = "digest-of-content-b"
)

func runAt(repo, commit string) QueryRun {
	return QueryRun{
		QueryID: "abom/edges", Repository: repo, DBCommit: commit,
		Pack: "graph", PackVersion: "1.0.0", PackDigest: "aaaa1111", CLIVersion: "2.26.4",
	}
}

func recordContent(t *testing.T, s *Store, repo, commit, digest string) {
	t.Helper()
	if err := s.RecordCommitContent(repo, commit, digest); err != nil {
		t.Fatalf("RecordCommitContent: %v", err)
	}
}

func existsForContent(t *testing.T, s *Store, q QueryRun, digest string) bool {
	t.Helper()
	got, err := s.QueryRunExistsForContent(q, digest)
	if err != nil {
		t.Fatalf("QueryRunExistsForContent: %v", err)
	}
	return got
}

// An action's release moves a tag to a new commit that rebuilt `dist/` and touched nothing the
// extractor reads. CodeQL is deterministic, so a database built from byte-identical content
// cannot answer differently — which is the same reasoning the commit-keyed skip already uses,
// with content standing where the commit stood.
func TestARunCountsForAnyCommitWithTheSameIndexedContent(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(runAt("octo/app", "commit-one")); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	recordContent(t, s, "octo/app", "commit-one", contentA)
	recordContent(t, s, "octo/app", "commit-two", contentA)

	if !existsForContent(t, s, runAt("octo/app", "commit-two"), contentA) {
		t.Error("a run against identical indexed content was not recognized")
	}
}

func TestARunDoesNotCountWhenTheIndexedContentDiffers(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(runAt("octo/app", "commit-one")); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	recordContent(t, s, "octo/app", "commit-one", contentA)
	recordContent(t, s, "octo/app", "commit-two", contentB)

	if existsForContent(t, s, runAt("octo/app", "commit-two"), contentB) {
		t.Error("changed content was treated as already analyzed")
	}
}

// Content equivalence replaces the commit in the identity and nothing else. A pack that has been
// edited is a different query, whatever the source it ran against.
func TestContentEquivalenceDoesNotWaiveTheRestOfTheIdentity(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(runAt("octo/app", "commit-one")); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	recordContent(t, s, "octo/app", "commit-one", contentA)

	edited := runAt("octo/app", "commit-two")
	edited.PackDigest = "cccc3333"
	if existsForContent(t, s, edited, contentA) {
		t.Error("an edited pack was treated as already run because the source matched")
	}
}

// Another repository's commit can carry the same content — a fork, or a vendored action — and
// its analysis is not this repository's.
func TestContentEquivalenceDoesNotCrossRepositories(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(runAt("octo/app", "commit-one")); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	recordContent(t, s, "octo/app", "commit-one", contentA)
	recordContent(t, s, "other/fork", "commit-two", contentA)

	if existsForContent(t, s, runAt("other/fork", "commit-two"), contentA) {
		t.Error("one repository's analysis was credited to another")
	}
}

// An empty digest is what a caller reports when it could not read the content. Matching on it
// would make every unreadable tree equivalent to every other.
func TestAnUnknownDigestMatchesNothing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(runAt("octo/app", "commit-one")); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	recordContent(t, s, "octo/app", "commit-one", "")

	if existsForContent(t, s, runAt("octo/app", "commit-two"), "") {
		t.Error("an unknown digest was treated as a match")
	}
}

// The pre-check runs before any fetch, so it can only use content it has already seen.
func TestAStoredDigestIsReadableByCommit(t *testing.T) {
	s := newTestStore(t)
	recordContent(t, s, "octo/app", "commit-one", contentA)

	got, ok, err := s.CommitContentDigest("octo/app", "commit-one")
	if err != nil {
		t.Fatalf("CommitContentDigest: %v", err)
	}
	if !ok || got != contentA {
		t.Errorf("digest = %q ok=%v, want %q true", got, ok, contentA)
	}

	if _, ok, err := s.CommitContentDigest("octo/app", "never-seen"); err != nil || ok {
		t.Errorf("an unseen commit reported a digest (ok=%v, err=%v)", ok, err)
	}
}

// ABOM fetches only the paths an actions database needs, so a commit names a repository's state
// and the filter names which of it was read. A run that saw the narrow tree has not answered for
// the wide one, whatever the commit says.
func TestARunUnderOneFilterDoesNotAnswerForAnother(t *testing.T) {
	s := newTestStore(t)
	narrow := runAt("octo/app", "commit-one")
	narrow.SourceFilter = "narrow"
	if _, err := s.RecordQueryRun(narrow); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	wide := runAt("octo/app", "commit-one")
	wide.SourceFilter = "wide"
	done, err := s.QueryRunExists(wide)
	if err != nil {
		t.Fatalf("QueryRunExists: %v", err)
	}
	if done {
		t.Error("a wider checkout of the same commit was treated as already analyzed, so the " +
			"paths the widening added would never be extracted")
	}
}

// A commit holds one content row, rewritten every time it is fetched. Widening the filter
// therefore re-measures the digest of a commit whose runs all happened against the smaller tree,
// and the equivalence join would credit those runs with content they never saw.
func TestContentEquivalenceDoesNotCrossFilters(t *testing.T) {
	s := newTestStore(t)
	narrow := runAt("octo/app", "commit-one")
	narrow.SourceFilter = "narrow"
	if _, err := s.RecordQueryRun(narrow); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	// The re-fetch under the wider filter rewrites what the commit is on record as holding.
	recordContent(t, s, "octo/app", "commit-one", contentB)

	wide := runAt("octo/app", "commit-one")
	wide.SourceFilter = "wide"
	if existsForContent(t, s, wide, contentB) {
		t.Error("a run against the narrow tree was credited with the wide tree's content")
	}
}
