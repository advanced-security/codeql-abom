package serve

import (
	"net/http"
	"testing"
)

// The details page asks a node's uses and versions together, and a reader moves between nodes.
// With nothing written in between, those requests stand on one loaded graph rather than each
// paying for the load, which was nearly all of their time.
func TestTheDrillDownSharesOneLoadedGraphAcrossRequests(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	for _, path := range []string{
		"/api/node/actions/checkout/-/uses",
		"/api/node/actions/checkout/-/versions",
		"/api/node/actions%2Fcheckout/-/uses?top=10",
		"/api/node/octo-org/app/.github/workflows/ci.yml/-/versions",
	} {
		if rec := get(t, srv, path); rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if got := srv.models.Loads(); got != 1 {
		t.Errorf("four drill-down requests loaded the graph %d times, want once", got)
	}
}
