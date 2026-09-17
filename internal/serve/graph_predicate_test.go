package serve

import (
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A predicate the API accepts and does not apply is worse than one it refuses.
//
// Measured on the reference corpus 2026-09-11: `/api/graph/full?origins=seed` parsed cleanly and
// returned all **12,550 nodes and 35,384 edges** — byte-identical to asking nothing. A gate asking
// for one tier would have been handed every tier with no way to tell, which is the same failure
// the 400 on an unknown filter exists to prevent, arriving through the front door.
func TestEveryPredicateTheGraphAcceptsReachesTheStore(t *testing.T) {
	for _, c := range []struct {
		query   string
		applied func(o store.FullGraphOptions) bool
	}{
		{"origins=seed", func(o store.FullGraphOptions) bool { return len(o.Origins) == 1 }},
		{"minReach=5", func(o store.FullGraphOptions) bool { return o.MinReach == 5 }},
		{"flags=" + viewspec.FlagUnanalyzed, func(o store.FullGraphOptions) bool { return o.UnanalyzedOnly }},
		{"steps=true", func(o store.FullGraphOptions) bool { return o.IncludeSteps }},
		{"scope=exposure", func(o store.FullGraphOptions) bool { return o.Scope == "exposure" }},
	} {
		spec, err := (&Server{}).graphPredicate(httptest.NewRequest("GET", "/api/graph/full?"+c.query, nil))
		if err != nil {
			t.Fatalf("parsing %q: %v", c.query, err)
		}
		opts, err := (&Server{}).graphOptions(spec)
		if err != nil {
			t.Fatalf("options for %q: %v", c.query, err)
		}
		if !c.applied(opts) {
			t.Errorf("%q parsed but reached the store as nothing: %+v", c.query, opts)
		}
	}
}

// The predicates that are deliberately *not* server-side, and why, so that adding one here is a
// decision rather than an oversight. They sever 74% to 99% of their own match's edges because the
// graph is bipartite by kind, and applying them here would ship a scatter instead of a graph.
func TestTheStructuralPredicatesStayInTheBrowser(t *testing.T) {
	for _, query := range []string{"kinds=workflow", "q=checkout", "rules=abom%2Fone"} {
		spec, err := (&Server{}).graphPredicate(httptest.NewRequest("GET", "/api/graph/full?"+query, nil))
		if err != nil {
			t.Fatalf("parsing %q: %v", query, err)
		}
		opts, err := (&Server{}).graphOptions(spec)
		if err != nil {
			t.Fatalf("options for %q: %v", query, err)
		}
		if len(opts.Origins) > 0 || opts.MinReach != 0 || opts.UnanalyzedOnly {
			t.Errorf("%q leaked into a server-side node filter: %+v", query, opts)
		}
	}
}
