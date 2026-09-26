package expand

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// packedResolver answers with queries that live in real pack directories, because a digest is
// read off the manifest beside the query and a query in no pack has none.
type packedResolver struct {
	queries map[string][]string
}

func (p *packedResolver) ResolveQueries(_ context.Context, suite string) ([]string, error) {
	return p.queries[suite], nil
}

func (p *packedResolver) ResolveMetadata(_ context.Context, path string) (runner.Metadata, error) {
	return runner.Metadata{ID: "abom/" + path, Tags: "abom-edges"}, nil
}

func (p *packedResolver) Version(_ context.Context) (string, error) { return "2.26.4", nil }

// identityOf resolves the bound suites through an expander of its own.
func identityOf(t *testing.T, queries map[string][]string, tiers map[string][]string) (string, []string) {
	t.Helper()
	h := newHarness(t)
	h.expander.Resolver = &packedResolver{queries: queries}
	for tier, suites := range tiers {
		if err := h.store.SetTierSuites(tier, suites); err != nil {
			t.Fatalf("SetTierSuites(%s): %v", tier, err)
		}
	}
	cliVersion, digests, err := h.expander.CurrentIdentity(context.Background())
	if err != nil {
		t.Fatalf("CurrentIdentity: %v", err)
	}
	return cliVersion, digests
}

// Selecting what is out of date needs the identity the bound suites resolve to *now*, and only
// the expansion can answer that: the store holds what was recorded, which after a CodeQL upgrade
// is precisely the stale answer the selection is trying to find.
func TestTheCurrentIdentityIsTheOneTheBoundSuitesResolveTo(t *testing.T) {
	root := t.TempDir()
	graph := queryInPack(t, root, "advanced-security/abom-graph", "2.26.4", "Edges.ql", "select 1")
	rules := queryInPack(t, root, "codeql/actions-queries", "0.6.34", "Unpinned.ql", "select 2")

	cliVersion, digests := identityOf(t,
		map[string][]string{"graph.qls": {graph}, "rules.qls": {rules}},
		map[string][]string{
			store.TierDirect:     {"graph.qls", "rules.qls"},
			store.TierTransitive: {"graph.qls"},
		})

	if cliVersion != "2.26.4" {
		t.Errorf("CodeQL version %q, want 2.26.4", cliVersion)
	}
	if len(digests) != 2 {
		t.Fatalf("got %d digests, want one per pack", len(digests))
	}
	if !slices.IsSorted(digests) {
		t.Errorf("digests %v are not sorted, so the same identity could compare unequal", digests)
	}
}

// A pack bound to two tiers is one pack. Counting it twice would make the identity depend on the
// binding rather than on what is actually being run.
func TestAPackSharedByTwoTiersIsCountedOnce(t *testing.T) {
	root := t.TempDir()
	graph := queryInPack(t, root, "advanced-security/abom-graph", "2.26.4", "Edges.ql", "select 1")

	_, digests := identityOf(t,
		map[string][]string{"graph.qls": {graph}},
		map[string][]string{
			store.TierDirect:     {"graph.qls"},
			store.TierTransitive: {"graph.qls"},
		})

	if len(digests) != 1 {
		t.Errorf("got %d digests for one pack in two tiers, want 1", len(digests))
	}
}

// Editing a query changes its pack's digest, which is the whole mechanism the selection rests on.
func TestEditingAQueryChangesTheCurrentIdentity(t *testing.T) {
	root := t.TempDir()
	path := queryInPack(t, root, "advanced-security/abom-graph", "2.26.4", "Edges.ql", "select 1")
	queries := map[string][]string{"graph.qls": {path}}
	tiers := map[string][]string{store.TierDirect: {"graph.qls"}}

	_, before := identityOf(t, queries, tiers)
	if err := os.WriteFile(path, []byte("select 2"), 0o644); err != nil {
		t.Fatalf("editing the query: %v", err)
	}
	_, after := identityOf(t, queries, tiers)

	if slices.Equal(before, after) {
		t.Error("editing a query left the identity unchanged, so nothing would be selected")
	}
}

// A server asks one expander for its identity again and again, and a pack edited while it runs has
// changed: an identity remembered from an earlier resolution is the stale answer a requeue exists to
// find, and would select nothing.
func TestAPackEditedWhileTheServerRunsMovesTheCurrentIdentity(t *testing.T) {
	path := queryInPack(t, t.TempDir(), "advanced-security/abom-graph", "2.26.4", "Edges.ql", "select 1")
	h := newHarness(t)
	h.expander.Resolver = &packedResolver{queries: map[string][]string{"graph.qls": {path}}}
	if err := h.store.SetTierSuites(store.TierDirect, []string{"graph.qls"}); err != nil {
		t.Fatal(err)
	}

	_, before, err := h.expander.CurrentIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("select 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, after, err := h.expander.CurrentIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, after) {
		t.Error("the expander answered with the digest it read before the edit, so a requeue would select nothing")
	}
}
