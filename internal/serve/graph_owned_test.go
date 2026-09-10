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
func TestTheOwnedScopeAnswersWithTheClosureRatherThanTheCorpus(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	whole := decode[store.Graph](t, get(t, srv, "/api/graph/full"))
	owned := decode[store.Graph](t, get(t, srv, "/api/graph/full?scope=owned"))

	keys := map[string]bool{}
	for _, n := range owned.Nodes {
		keys[n.Key] = true
	}
	if !keys["actions/checkout"] {
		t.Error("octo-org/app reaches actions/checkout, so the closure holds it; the scope dropped it")
	}
	if keys["stranger/thing"] {
		t.Error("stranger/thing is in no declared account and the owned scope kept it")
	}
	if len(owned.Nodes) >= len(whole.Nodes) {
		t.Errorf("owned scope drew %d of the corpus's %d nodes; it must be narrower",
			len(owned.Nodes), len(whole.Nodes))
	}
}
