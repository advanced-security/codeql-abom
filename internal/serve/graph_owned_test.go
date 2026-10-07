package serve

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The scope is only useful if the server computes the closure. The store cannot: it takes the
// repository list as an argument, so a handler that forgets to fill it still answers, with a graph
// made only of the declared repositories that attachOwnedRepositories draws whatever the scope.
//
// actions/checkout is the assertion that matters. It belongs to nobody declared and octo-org/app
// reaches it, so it is present only once the closure has been computed and applied — a scope that
// merely filtered by declared repository would drop it.
func TestTheSupplyChainScopeAnswersWithTheClosureRatherThanTheCorpus(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	whole := decode[store.Graph](t, get(t, srv, "/api/graph/full"))
	closure := decode[store.Graph](t, get(t, srv, "/api/graph/full?scope=supply-chain"))

	keys := map[string]bool{}
	for _, n := range closure.Nodes {
		keys[n.Key] = true
	}
	if !keys["actions/checkout"] {
		t.Error("octo-org/app reaches actions/checkout, so the closure holds it; the scope dropped it")
	}
	if keys["stranger/thing"] {
		t.Error("stranger/thing is in no declared account and the supply chain kept it")
	}
	if len(closure.Nodes) >= len(whole.Nodes) {
		t.Errorf("the supply chain drew %d of the corpus's %d nodes; it must be narrower",
			len(closure.Nodes), len(whole.Nodes))
	}
}

// What you own is the declared repositories' own nodes, through the API as in the store: the
// action they run is somebody else's, and every node drawn reads owned.
func TestTheOwnedScopeAnswersWithOnlyWhatTheDeclarationHolds(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	owned := decode[store.Graph](t, get(t, srv, "/api/graph/full?scope=owned"))
	if len(owned.Nodes) == 0 {
		t.Fatal("the owned scope drew nothing, so this proves nothing")
	}
	for _, n := range owned.Nodes {
		if !n.Owned {
			t.Errorf("the owned scope drew %s (%s), which is not yours", n.Key, n.Kind)
		}
		if n.Key == "actions/checkout" || n.Key == "stranger/thing" {
			t.Errorf("the owned scope drew %s, which no declared repository holds", n.Key)
		}
	}
}
