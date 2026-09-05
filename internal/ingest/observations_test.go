package ingest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func observationStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// This is the whole point of the spine, so it is tested as the thing it promises: a query that
// did not exist when the ingester was written contributes facts, and no Go was added for it.
// If this ever needs a code change to pass, the design has failed.
func TestAQueryNobodyWroteCodeForCanStillContributeFacts(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{
		QueryID: "someone/invented-this-later", QueryVersion: "1",
		Repository: "octo/app", DBCommit: "abc",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectKey","kind":"String"},
		{"name":"name","kind":"String"},
		{"name":"value","kind":"String"},
		{"name":"valueNum","kind":"Float"}],
		"tuples":[["actions/checkout","step-reachable","true",0.75]]}}`

	i := New(s, store.NewWriter(s, 0))
	n, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	})
	if err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}
	if n != 1 {
		t.Fatalf("ingested %d observations, want 1", n)
	}

	got, err := s.ObservationsOf(store.SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d, want 1", len(got))
	}
	if got[0].QueryID != "someone/invented-this-later" {
		t.Errorf("QueryID = %q, want the query that produced it", got[0].QueryID)
	}
	if got[0].Name != "step-reachable" || got[0].Value != "true" {
		t.Errorf("got %+v, want the fact the query stated", got[0])
	}
	if !got[0].HasNumber || got[0].Number != 0.75 {
		t.Errorf("Number = %v (has=%v), want the measured value", got[0].Number, got[0].HasNumber)
	}
}

// The simplest useful observation says only that something is true of something. Demanding more
// columns would rule out exactly the queries most likely to be written quickly.
func TestSubjectAndNameAreEnough(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{
		QueryID: "abom/minimal", Repository: "octo/app",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectKey","kind":"String"},
		{"name":"name","kind":"String"}],
		"tuples":[["actions/checkout","looked-at"]]}}`

	i := New(s, store.NewWriter(s, 0))
	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	}); err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}
	got, err := s.ObservationsOf(store.SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 || got[0].Name != "looked-at" {
		t.Errorf("got %+v, want the one fact stated", got)
	}
}

// A result that cannot be attached to anything would be a fact about nothing, and saying so at
// ingest is far cheaper than discovering it as a silently empty view later.
func TestAQueryMissingTheContractIsRefusedWithTheColumnItNeeds(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{QueryID: "abom/bad", Repository: "octo/app"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[{"name":"whatever","kind":"String"}],
		"tuples":[["actions/checkout"]]}}`

	i := New(s, store.NewWriter(s, 0))
	_, err = i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	})
	if err == nil {
		t.Fatal("a query missing the contract was accepted")
	}
	if !strings.Contains(err.Error(), ColSubjectKey) {
		t.Errorf("error %q does not name the column the query must select", err)
	}
}

// An unattributed fact is the one thing this table may never hold, so it is refused before it
// can be written rather than trusted to a caller remembering.
func TestObservationsCannotBeIngestedWithoutAQueryRun(t *testing.T) {
	s := observationStore(t)
	i := New(s, store.NewWriter(s, 0))
	bqrs := `{"#select":{"columns":[{"name":"subjectKey","kind":"String"},
		{"name":"name","kind":"String"}],"tuples":[["a","b"]]}}`

	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app",
	}); err == nil {
		t.Error("observations were accepted with nothing to attribute them to")
	}
}

// A query runs against one repository's database and cannot know which repository that is, so
// it names a job by where it sits and the ingester supplies the rest. Without this, AbomSurface
// could not attach a single fact to the job it measured.
func TestAQueryMayNameASubjectByWhereItSits(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{QueryID: "abom/surface", Repository: "octo/app"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectPath","kind":"String"},
		{"name":"subjectJob","kind":"String"},
		{"name":"name","kind":"String"},
		{"name":"value","kind":"String"}],
		"tuples":[[".github/workflows/ci.yml","build","privileged","true"]]}}`

	i := New(s, store.NewWriter(s, 0))
	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	}); err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}

	// The same key the graph builds, so the fact lands on the node a reader is looking at.
	want := normalize.JobKey("octo/app", ".github/workflows/ci.yml", "build")
	got, err := s.ObservationsOf(store.SubjectNode, want)
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 || got[0].Name != "privileged" || got[0].Value != "true" {
		t.Errorf("got %+v for %q, want the fact attached to that job", got, want)
	}
}

// Without a job the subject is the workflow itself, which is what a query measuring a file
// rather than a job inside it needs.
func TestASubjectWithoutAJobIsTheWorkflow(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{QueryID: "abom/wf", Repository: "octo/app"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectPath","kind":"String"},
		{"name":"name","kind":"String"}],
		"tuples":[[".github/workflows/ci.yml","looked-at"]]}}`

	i := New(s, store.NewWriter(s, 0))
	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	}); err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}

	want := normalize.WorkflowKey("octo/app", ".github/workflows/ci.yml")
	got, err := s.ObservationsOf(store.SubjectNode, want)
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %+v for %q, want the fact attached to the workflow", got, want)
	}
}

// Steps are keyed by position because `id:` is optional and usually absent, so a query about a
// step has to be able to say which one. Without this a condition could only be attached to the
// whole job, which is a different and much weaker claim.
func TestAQueryMayNameAStepByItsPosition(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{QueryID: "abom/conditions", Repository: "octo/app"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectPath","kind":"String"},
		{"name":"subjectJob","kind":"String"},
		{"name":"subjectStep","kind":"Integer"},
		{"name":"name","kind":"String"},
		{"name":"value","kind":"String"}],
		"tuples":[[".github/workflows/ci.yml","build",2,"condition","github.ref == 'refs/heads/main'"]]}}`

	i := New(s, store.NewWriter(s, 0))
	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	}); err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}

	want := normalize.StepKey("octo/app", ".github/workflows/ci.yml", "build", 2)
	got, err := s.ObservationsOf(store.SubjectNode, want)
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 || got[0].Value != "github.ref == 'refs/heads/main'" {
		t.Errorf("got %+v for %q, want the condition recorded verbatim", got, want)
	}
}

// A composite action's steps sit in a `runs:` block, which has no job to name, so the position
// is the only thing distinguishing them. Falling back to the file when the job is empty put
// every one of them on one key that names no node — and the uniqueness constraint then kept a
// single row per file. These are the conditions that matter most, because a composite action's
// steps run inside every caller.
func TestAStepInACompositeActionIsNamedByPositionRatherThanItsFile(t *testing.T) {
	s := observationStore(t)
	run, err := s.RecordQueryRun(store.QueryRun{QueryID: "abom/conditions", Repository: "octo/app"})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	bqrs := `{"#select":{"columns":[
		{"name":"subjectPath","kind":"String"},
		{"name":"subjectJob","kind":"String"},
		{"name":"subjectStep","kind":"Integer"},
		{"name":"name","kind":"String"},
		{"name":"value","kind":"String"}],
		"tuples":[
			[".github/actions/greet/action.yml","",0,"condition","runner.os == 'Linux'"],
			[".github/actions/greet/action.yml","",1,"condition","runner.os != 'Linux'"]]}}`

	i := New(s, store.NewWriter(s, 0))
	if _, err := i.ObservationsFromBQRS(strings.NewReader(bqrs), Options{
		Repository: "octo/app", QueryRunID: run,
	}); err != nil {
		t.Fatalf("ObservationsFromBQRS: %v", err)
	}

	// The same key the graph gives a composite action's step, so the fact lands on the node a
	// reader is looking at rather than beside it.
	for i, condition := range []string{"runner.os == 'Linux'", "runner.os != 'Linux'"} {
		want := normalize.StepKey("octo/app", ".github/actions/greet/action.yml", "", i)
		got, err := s.ObservationsOf(store.SubjectNode, want)
		if err != nil {
			t.Fatalf("ObservationsOf: %v", err)
		}
		if len(got) != 1 || got[0].Value != condition {
			t.Errorf("got %+v for %q, want %q", got, want, condition)
		}
	}
}

// Routing is decided by what the query declares, because a filename says nothing reliable and
// renaming a query must not change how its results are read.
func TestResultsAreRoutedByWhatTheQueryDeclares(t *testing.T) {
	cases := []struct {
		name string
		md   runner.Metadata
		want Shape
	}{
		{"a security query", runner.Metadata{ID: "actions/unpinned-tag", Kind: "problem"}, ShapeFindings},
		{"a path query", runner.Metadata{ID: "actions/taint", Kind: "path-problem"}, ShapeFindings},
		{"an AST query", runner.Metadata{ID: "actions/tools/print-ast", Kind: "graph"}, ShapeGraph},
		{"the edge extractor", runner.Metadata{ID: "abom/edges", Tags: "abom " + TagEdges}, ShapeEdges},
		{"an observation query", runner.Metadata{ID: "abom/reach", Tags: TagObservation}, ShapeObservations},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ShapeOf(c.md)
			if err != nil {
				t.Fatalf("ShapeOf: %v", err)
			}
			if got != c.want {
				t.Errorf("ShapeOf = %q, want %q", got, c.want)
			}
		})
	}
}

// Guessing would mean silently mis-ingesting a query someone added in good faith, which is worse
// than refusing it: a wrong graph is harder to notice than a missing one.
func TestAQueryThatDeclaresNothingUsableIsRefused(t *testing.T) {
	_, err := ShapeOf(runner.Metadata{ID: "abom/mystery"})
	if err == nil {
		t.Fatal("a query declaring neither a kind nor a contract tag was accepted")
	}
	for _, want := range []string{"abom/mystery", TagEdges, TagObservation} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
