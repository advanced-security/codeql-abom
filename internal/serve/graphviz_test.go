package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A server that has lost Graphviz is a broken installation: the layout answers 503 naming the
// remedy, never a placement nobody made.
func TestTheLayoutAnswersUnavailableWithTheRemedyWhenGraphvizIsGone(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	t.Setenv("PATH", t.TempDir())

	rec := get(t, srv, "/api/graph/layout")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "install Graphviz") {
		t.Errorf("the refusal does not name the remedy: %s", rec.Body.String())
	}
}

// And with Graphviz present, every placement says Graphviz made it.
func TestTheLayoutIsPlacedByGraphviz(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	rec := get(t, srv, "/api/graph/layout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var placed GraphLayout
	if err := json.Unmarshal(rec.Body.Bytes(), &placed); err != nil {
		t.Fatal(err)
	}
	if placed.ProducedBy != "graphviz" || len(placed.Positions) == 0 {
		t.Errorf("placed by %q with %d positions", placed.ProducedBy, len(placed.Positions))
	}
}
