package expand

import (
	"context"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// fakeResolver stands in for the CodeQL CLI's own view of a suite.
type fakeResolver struct {
	queries []string
	meta    map[string]runner.Metadata
	asked   int
}

func (f *fakeResolver) ResolveQueries(context.Context, string) ([]string, error) {
	f.asked++
	return f.queries, nil
}

func (f *fakeResolver) ResolveMetadata(_ context.Context, path string) (runner.Metadata, error) {
	return f.meta[path], nil
}

func (f *fakeResolver) Version(_ context.Context) (string, error) { return "2.26.4", nil }

// Adding a query to a suite has to be enough to make it run. If the set of queries were compiled
// in, every new query would need a release, which is the opposite of an inspection engine meant
// to be reprogrammed.
func TestASuiteDecidesWhatRuns(t *testing.T) {
	e := &Expander{Resolver: &fakeResolver{
		queries: []string{"/q/AbomEdges.ql", "/q/Reach.ql"},
		meta: map[string]runner.Metadata{
			"/q/AbomEdges.ql": {ID: "abom/edges", Tags: ingest.TagEdges},
			"/q/Reach.ql":     {ID: "abom/reach", Tags: ingest.TagObservation},
		},
	}}

	planned, err := e.plan(context.Background(), Options{Suite: "abom-dependency-graph.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(planned.bqrs) != 2 {
		t.Fatalf("planned %d queries, want both the suite names", len(planned.bqrs))
	}
	if planned.bqrs[0].shape != ingest.ShapeEdges ||
		planned.bqrs[1].shape != ingest.ShapeObservations {
		t.Errorf("shapes = %q/%q, want each read as the query declares",
			planned.bqrs[0].shape, planned.bqrs[1].shape)
	}
}

// Resolving a suite shells out to the CLI. Doing that per repository would cost more than the
// queries across a crawl of thousands, and the suite cannot change mid-round anyway.
func TestASuiteIsResolvedOncePerRoundNotPerRepository(t *testing.T) {
	r := &fakeResolver{
		queries: []string{"/q/AbomEdges.ql"},
		meta:    map[string]runner.Metadata{"/q/AbomEdges.ql": {ID: "abom/edges", Tags: ingest.TagEdges}},
	}
	e := &Expander{Resolver: r}

	for i := 0; i < 3; i++ {
		if _, err := e.plan(context.Background(), Options{Suite: "s.qls"}); err != nil {
			t.Fatalf("plan: %v", err)
		}
	}
	if r.asked != 3 {
		t.Fatalf("resolver asked %d times for 3 rounds, want one per round", r.asked)
	}
}

// A single query path stays supported: `expand` and `build` pass one, and resolving a suite to
// run one thing would be cost without benefit.
func TestASingleQueryStillNeedsNoSuite(t *testing.T) {
	e := &Expander{}
	planned, err := e.plan(context.Background(), Options{
		QueryPath: "/q/AbomEdges.ql", QueryID: "abom/edges",
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(planned.bqrs) != 1 || planned.bqrs[0].shape != ingest.ShapeEdges {
		t.Errorf("planned %+v, want the one query read as edges", planned)
	}
}

// Findings come from `database analyze` as SARIF, a different lane entirely. A suite may mix
// them in legitimately, so they leave the BQRS lane — but they must be *routed*, not dropped:
// silently discarding them made the `direct` binding promise a findings pass it never ran.
func TestFindingsQueriesAreLeftToTheirOwnLane(t *testing.T) {
	e := &Expander{Resolver: &fakeResolver{
		queries: []string{"/q/AbomEdges.ql", "/q/Unpinned.ql"},
		meta: map[string]runner.Metadata{
			"/q/AbomEdges.ql": {ID: "abom/edges", Tags: ingest.TagEdges},
			"/q/Unpinned.ql":  {ID: "actions/unpinned-tag", Kind: "problem"},
		},
	}}

	planned, err := e.plan(context.Background(), Options{Suite: "mixed.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(planned.bqrs) != 1 || planned.bqrs[0].id != "abom/edges" {
		t.Errorf("bqrs lane %+v, want only the query this lane can read", planned.bqrs)
	}
	if len(planned.findings) != 1 || planned.findings[0] != "/q/Unpinned.ql" {
		t.Errorf("findings lane %v, want the problem query routed rather than dropped",
			planned.findings)
	}
}

// A query nobody can read is refused before the round starts rather than part way through it,
// and the message names the suite because that is where someone will go to take it back out.
func TestAnUnreadableQueryStopsTheRoundAndNamesTheSuite(t *testing.T) {
	e := &Expander{Resolver: &fakeResolver{
		queries: []string{"/q/Mystery.ql"},
		meta:    map[string]runner.Metadata{"/q/Mystery.ql": {ID: "someone/mystery"}},
	}}

	_, err := e.plan(context.Background(), Options{Suite: "abom-dependency-graph.qls"})
	if err == nil {
		t.Fatal("a query with no declared shape was planned")
	}
	for _, want := range []string{"abom-dependency-graph.qls", "someone/mystery"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Two queries in one suite must not write to the same file, and query ids carry slashes.
func TestQueryIdsBecomeDistinctFilenames(t *testing.T) {
	a, b := safeName("actions/tools/print-ast"), safeName("actions/tools/print-cfg")
	if a == b {
		t.Fatal("two query ids produced the same filename")
	}
	for _, name := range []string{a, b} {
		if strings.ContainsAny(name, `/\: `) {
			t.Errorf("%q is not usable as a filename", name)
		}
	}
}
