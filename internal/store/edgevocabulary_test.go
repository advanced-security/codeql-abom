package store

import (
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// The filter language decides which values exist; the store decides what they do. Those are two
// files, and a value renamed in one is a filter that silently stops narrowing in the other.
//
// `boundaryFilter` returns an empty SQL fragment for a value it does not recognize — correct, so a
// query string cannot reach the statement — which means drift here does not fail, it just stops
// filtering. `scopeFilter` is worse: it falls through to no scope at all.
func TestTheStoreSpellsEveryValueTheLanguageAccepts(t *testing.T) {
	for _, c := range []struct{ store, language string }{
		{BoundarySameRepository, viewspec.BoundarySameRepository},
		{BoundarySameOwner, viewspec.BoundarySameOwner},
		{BoundaryCrossOwner, viewspec.BoundaryCrossOwner},
		{ScopeOwned, viewspec.ScopeOwned},
		{ScopeSupplyChain, viewspec.ScopeSupplyChain},
		{ScopeExposure, viewspec.ScopeExposure},
		{RefStyleSHA, viewspec.RefStyleSHA},
		{RefStyleTag, viewspec.RefStyleTag},
	} {
		if c.store != c.language {
			t.Errorf("the store says %q and the language says %q", c.store, c.language)
		}
	}
}

// And the filter actually narrows for each of them, or the agreement above is between two spellings
// of a no-op.
func TestEveryBoundaryTheLanguageAcceptsProducesAFilter(t *testing.T) {
	for _, value := range []string{
		viewspec.BoundarySameRepository, viewspec.BoundarySameOwner, viewspec.BoundaryCrossOwner,
	} {
		if boundaryFilter(value) == "" {
			t.Errorf("boundary=%q parses and then filters nothing", value)
		}
	}
	if boundaryFilter("nonsense") != "" {
		t.Error("an unrecognized value reached the SQL")
	}
}

// The same agreement for the kinds of relationship. The store reads `edge_kind` out of the CodeQL
// results, so a value the language offers and the results never produce is a filter that always
// answers empty, and one the results produce and the language cannot name is a relationship nobody
// can ask about.
func TestTheStoreSpellsEveryKindTheLanguageAccepts(t *testing.T) {
	for _, c := range []struct{ store, language string }{
		{EdgeKindStepUsesAction, viewspec.EdgeKindStepUsesAction},
		{EdgeKindJobNeedsJob, viewspec.EdgeKindJobNeedsJob},
		{EdgeKindJobUsesReusableWorkflow, viewspec.EdgeKindJobUsesReusableWorkflow},
		{EdgeKindCompositeStepUsesAction, viewspec.EdgeKindCompositeStepUsesAction},
	} {
		if c.store != c.language {
			t.Errorf("the store says %q and the language says %q", c.store, c.language)
		}
	}
}

// And each of them narrows, or the agreement above is between two spellings of a no-op.
func TestEveryKindTheLanguageAcceptsProducesAFilter(t *testing.T) {
	for _, kind := range viewspec.EdgeKinds() {
		if edgeKindFilter([]string{kind}) == "" {
			t.Errorf("edgeKinds=%q parses and then filters nothing", kind)
		}
	}
	if edgeKindFilter(nil) != "" {
		t.Error("asking nothing narrowed something")
	}
	// A kind nothing produces is a question with an empty answer, not a question that was
	// dropped: an unrecognized value must never widen the match back to everything.
	if f := edgeKindFilter([]string{"nonsense"}); f == "" || strings.Contains(f, "nonsense") {
		t.Errorf("edgeKinds=nonsense produced %q, want a filter that matches nothing", f)
	}
}
