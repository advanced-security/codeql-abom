package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// tierResolver answers without a CodeQL CLI, and records which suites were asked for so the
// binding can be observed rather than inferred.
type tierResolver struct {
	asked   []string
	queries map[string][]string
}

func (f *tierResolver) ResolveQueries(_ context.Context, suite string) ([]string, error) {
	f.asked = append(f.asked, suite)
	return f.queries[suite], nil
}

func (f *tierResolver) ResolveMetadata(_ context.Context, path string) (runner.Metadata, error) {
	return runner.Metadata{ID: "abom/" + path, Tags: "abom-edges"}, nil
}

func (f *tierResolver) Version(_ context.Context) (string, error) { return "2.26.4", nil }

func boundHarness(t *testing.T) (*harness, *tierResolver) {
	t.Helper()
	h := newHarness(t)
	r := &tierResolver{queries: map[string][]string{
		"graph.qls":    {"Edges.ql"},
		"findings.qls": {"Findings.ql"},
	}}
	h.expander.Resolver = r
	// Owned gets the broad pass, which is the binding's whole reason: everything on what you own,
	// the minimum on what you reach through three intermediaries.
	if err := h.store.SetTierSuites(store.TierOwned, []string{"graph.qls", "findings.qls"}); err != nil {
		t.Fatalf("SetTierSuites(owned): %v", err)
	}
	if err := h.store.SetTierSuites(store.TierDirect, []string{"graph.qls", "findings.qls"}); err != nil {
		t.Fatalf("SetTierSuites(direct): %v", err)
	}
	if err := h.store.SetTierSuites(store.TierTransitive, []string{"graph.qls"}); err != nil {
		t.Fatalf("SetTierSuites(transitive): %v", err)
	}
	return h, r
}

// The reason the binding exists: "broad pass on what we own, minimum on what we reach through
// three intermediaries" was not expressible while one flag named one suite for everything.
func TestEachTierRunsWhatItIsBoundTo(t *testing.T) {
	h, _ := boundHarness(t)

	plans, err := h.expander.plansByTier(context.Background(), Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	if len(plans[store.TierDirect].bqrs) != 2 {
		t.Errorf("direct planned %d queries, want the two suites it is bound to",
			len(plans[store.TierDirect].bqrs))
	}
	if len(plans[store.TierTransitive].bqrs) != 1 {
		t.Errorf("transitive planned %d queries, want the one suite it is bound to",
			len(plans[store.TierTransitive].bqrs))
	}
}

// Resolving a suite shells out, and a crawl claims batches from both tiers every round. A suite
// two tiers share must still only be resolved once.
func TestASharedSuiteIsResolvedOncePerRound(t *testing.T) {
	h, r := boundHarness(t)
	if _, err := h.expander.plansByTier(context.Background(), Options{WorkDir: t.TempDir()}); err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	seen := 0
	for _, s := range r.asked {
		if s == "graph.qls" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("graph.qls was resolved %d times, want once even though both tiers bind it", seen)
	}
}

// The flag has to keep winning: it is how an operator runs something once without editing
// stored configuration.
func TestAnExplicitSuiteOverridesTheStoredBinding(t *testing.T) {
	h, _ := boundHarness(t)

	plans, err := h.expander.plansByTier(context.Background(),
		Options{WorkDir: t.TempDir(), Suite: "findings.qls"})
	if err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		if len(plans[tier].bqrs) != 1 || plans[tier].bqrs[0].path != "Findings.ql" {
			t.Errorf("%s planned %+v, want only the suite named on the command line",
				tier, plans[tier])
		}
	}
}

// A tier bound to nothing is an instruction, not an oversight, so it must plan nothing rather
// than falling back to a default that was deliberately removed.
func TestATierBoundToNothingPlansNothing(t *testing.T) {
	h, _ := boundHarness(t)
	if err := h.store.SetTierSuites(store.TierTransitive, nil); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}

	plans, err := h.expander.plansByTier(context.Background(), Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	if !planIsEmpty(plans[store.TierTransitive]) {
		t.Errorf("transitive planned %+v, want nothing", plans[store.TierTransitive])
	}
}

// Depth is what separates a dependency a repository under review names itself from one that
// arrives through somebody else's intermediary, and it is what chooses the pass.
func TestABatchIsPlannedByTheShallowestDepthItHolds(t *testing.T) {
	if got := tierOf(batch{depth: 1}, nil); got != store.TierDirect {
		t.Errorf("depth 1 is the %q tier, got %q", store.TierDirect, got)
	}
	if got := tierOf(batch{depth: 2}, nil); got != store.TierTransitive {
		t.Errorf("depth 2 is the %q tier, got %q", store.TierTransitive, got)
	}
}

// Owning a dependency is a different answer to "what can you do about this" than naming it, and
// the graph places an owned action at whatever depth it happens to be reached.
func TestAnOwnedBatchIsPlannedAsOwnedAtAnyDepth(t *testing.T) {
	owned := map[string]bool{"octo-org": true}
	for _, depth := range []int{1, 4} {
		got := tierOf(batch{owner: "Octo-Org", depth: depth}, owned)
		if got != store.TierOwned {
			t.Errorf("an owned batch at depth %d is the %q tier, got %q",
				depth, store.TierOwned, got)
		}
	}
	if got := tierOf(batch{owner: "someone-else", depth: 1}, owned); got != store.TierDirect {
		t.Errorf("an unowned batch is the %q tier, got %q", store.TierDirect, got)
	}
}

func planIsEmpty(p tierPlan) bool { return len(p.bqrs) == 0 && len(p.findings) == 0 }
