package store

import (
	"reflect"
	"testing"
)

// Two closures of one repository at one commit staged different callees, so they are two
// databases and two runs. One run would let the second closure's results arrive attributed to
// the inputs of the first.
func TestAClosureRunIsIdentifiedByWhatItStaged(t *testing.T) {
	s := newTestStore(t)
	base := QueryRun{
		QueryID: "abom-findings.qls", Repository: "octo/app", DBCommit: "a1",
		AnalysisMode: AnalysisModeExtended, RecordedBy: RecordedByClosure,
	}
	one, two := base, base
	one.Closure, two.Closure = "digest-one", "digest-two"

	first, err := s.RecordQueryRun(one)
	if err != nil {
		t.Fatalf("recording the first closure: %v", err)
	}
	second, err := s.RecordQueryRun(two)
	if err != nil {
		t.Fatalf("recording the second closure: %v", err)
	}
	again, err := s.RecordQueryRun(one)
	if err != nil {
		t.Fatalf("recording the first closure again: %v", err)
	}
	if first == second {
		t.Errorf("two closures with different staged callees are one run (%d)", first)
	}
	if first != again {
		t.Errorf("the same closure recorded twice is two runs (%d, %d)", first, again)
	}
	var digest string
	if err := s.db.QueryRow(`SELECT closure_digest FROM abom_query_run WHERE row_id = ?`,
		second).Scan(&digest); err != nil {
		t.Fatalf("reading the digest: %v", err)
	}
	if digest != "digest-two" {
		t.Errorf("the run records digest %q, want the one it was built from", digest)
	}
}

// A manifest describes a database holding more than one repository. On a regular run it would
// claim inputs the database never had.
func TestAManifestBelongsOnlyToAnExtendedRun(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "a1",
		AnalysisMode: AnalysisModeRegular, Closure: "digest",
	}); err == nil {
		t.Fatal("a regular run was recorded with a closure manifest")
	}
}

// Every finding names its producer and how to reproduce it. For a closure that is the set of
// repositories and commits staged, which the digest alone cannot give back.
func TestAClosureManifestNamesEveryStagedCommit(t *testing.T) {
	s := newTestStore(t)
	want := ClosureManifest{
		Digest: "d1", Root: "octo/app", RootCommit: "a1", Anchored: true,
		Members: []ClosureMember{
			{Repository: "vendor/comp", Commit: "c1", Kind: "composite", Path: "action.yml",
				StagedPath: ".github/actions/external/vendor/comp/action.yml"},
			{Repository: "vendor/rw", Commit: "r1", Kind: "reusable-workflow",
				Path:       ".github/workflows/reuse.yml",
				StagedPath: ".github/workflows/external/vendor/rw/.github/workflows/reuse.yml"},
		},
	}
	for range 2 {
		if err := s.RecordClosure(want); err != nil {
			t.Fatalf("RecordClosure: %v", err)
		}
	}
	got, ok, err := s.ClosureManifestOf("d1")
	if err != nil || !ok {
		t.Fatalf("ClosureManifestOf: %v, %v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if _, ok, err := s.ClosureManifestOf("nothing"); err != nil || ok {
		t.Errorf("an unknown digest read as recorded (%v, %v)", ok, err)
	}
}

// Attribution reads the corpus's own spelling of a repository and never creates one. A row it
// created would be a repository nothing analyzed, counted by every total that counts repositories.
func TestARepositoryIsFoundByAnySpellingAndNeverCreated(t *testing.T) {
	s := newTestStore(t)
	id, err := s.InsertUpstreamRepository("Vendor/Comp")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	got, name, ok, err := s.RepositoryNamed("vendor/comp")
	if err != nil || !ok {
		t.Fatalf("RepositoryNamed: %v, %v", ok, err)
	}
	if got != id || name != "Vendor/Comp" {
		t.Errorf("found %d %q, want %d %q", got, name, id, "Vendor/Comp")
	}
	if _, _, ok, err := s.RepositoryNamed("vendor/none"); err != nil || ok {
		t.Errorf("a repository nobody recorded was found (%v, %v)", ok, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("looking up repositories left %d rows, want the one recorded", n)
	}
}

// The commit a closure is rooted at, or stages a callee at when no lock chose one, is the one the
// crawl analyzed for that queue row. A row nobody analyzed has none, which is not a commit of "".
func TestTheAnalyzedCommitIsWhatTheQueueRowWasAnalyzedAt(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB().Exec(`INSERT INTO abom_frontier
		(node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha)
		VALUES ('Octo/App', '', 'Octo', 'App', 0, 'done', 'now', 'ABC123'),
		       ('vendor/comp', 'v1', 'vendor', 'comp', 1, 'pending', 'now', NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key, ref, want string
		ok             bool
	}{
		{"Octo/App", "", "abc123", true},
		{"octo/app", "", "abc123", true},
		{"vendor/comp", "v1", "", false},
		{"vendor/none", "v1", "", false},
	} {
		got, ok, err := s.AnalyzedCommit(c.key, c.ref)
		if err != nil {
			t.Fatalf("AnalyzedCommit: %v", err)
		}
		if got != c.want || ok != c.ok {
			t.Errorf("AnalyzedCommit(%s, %q) = %q, %v; want %q, %v", c.key, c.ref, got, ok, c.want, c.ok)
		}
	}
}
