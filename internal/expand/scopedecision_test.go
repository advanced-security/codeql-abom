package expand

import (
	"context"
	"errors"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Every tier in a round is read from ownership, so a round run while the declaration does not verify
// would analyze your repositories as somebody else's dependencies. It claims nothing until
// the declaration is decided again, and says why.
func TestARoundClaimsNothingWhileTheDeclarationDoesNotVerify(t *testing.T) {
	h := newHarness(t)
	owned := item("acme", "service", "", 0)
	h.own(owned)
	h.enqueueUnowned(owned)
	if _, err := h.store.DB().Exec(`UPDATE abom_owned_scope SET includes_only = 1`); err != nil {
		t.Fatal(err)
	}

	_, err := h.expander.Run(context.Background(), Options{QueryPath: "AbomEdges.ql", QueryID: "abom/edges",
		WorkDir: t.TempDir(), Limit: 10})
	var undecided *scopeNotDecided
	if !errors.As(err, &undecided) || undecided.Verdict.State != store.ScopeUnverified {
		t.Fatalf("Run = %v; want the round refused for an undecided declaration", err)
	}
	if len(h.fetched) != 0 || len(h.built) != 0 {
		t.Errorf("a round with an undecided declaration fetched %v and built %v", h.fetched, h.built)
	}
	counts, err := h.store.FrontierCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["queued"] != 1 || counts["running"] != 0 {
		t.Errorf("frontier = %v; want the owned row left pending, unclaimed", counts)
	}
}
