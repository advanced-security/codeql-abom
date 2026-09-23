package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type catalogAnswer struct {
	Queries []store.CatalogQuery `json:"queries"`
	Stages  []string             `json:"stages"`
}

func readCatalog(t *testing.T, s *Server) catalogAnswer {
	t.Helper()
	w := httptest.NewRecorder()
	s.queries(w, httptest.NewRequest("GET", "/api/queries", nil))
	if w.Code != 200 {
		t.Fatalf("/api/queries answered %d: %s", w.Code, w.Body.String())
	}
	var got catalogAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	return got
}

func advance(t *testing.T, s *Server, body string) int {
	t.Helper()
	w := httptest.NewRecorder()
	s.advanceQuery(w, httptest.NewRequest("POST", "/api/queries/stage", bytes.NewBufferString(body)))
	return w.Code
}

// A page cannot offer a lifecycle it has to hard-code. The stages travel with the catalog so the
// interface and the store cannot come to disagree about what the lifecycle is.
func TestTheCatalogCarriesTheLifecycleItIsStagedAgainst(t *testing.T) {
	s, _ := newServer(t)
	got := readCatalog(t, s)
	if len(got.Stages) == 0 {
		t.Error("the catalog names no stages, so a reader would have to invent the lifecycle")
	}
	if got.Queries == nil {
		t.Error("queries marshaled as null rather than an empty list, which the client throws on")
	}
}

// The refusal belongs at the boundary: a version in a state nothing downstream reads is cheapest
// to catch before it is stored.
func TestAdvancingToAStageOutsideTheLifecycleIsRefused(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s,
		`{"queryId":"abom/edges","queryVersion":"v1","stage":"shipped","by":"operator"}`); code != http.StatusBadRequest {
		t.Errorf("a stage outside the lifecycle answered %d, want 400", code)
	}
}

// Advancement is an act somebody signs, so one with nobody named is refused rather than recorded
// against an anonymous author.
func TestAdvancingWithNobodyNamedIsRefused(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s,
		`{"queryId":"abom/edges","queryVersion":"v1","stage":"test","by":""}`); code != http.StatusBadRequest {
		t.Errorf("an unsigned advance answered %d, want 400", code)
	}
}

// A malformed body is an error rather than a silently ignored write, or a caller would believe it
// had staged something it had not.
func TestAMalformedAdvanceIsAnErrorRatherThanANoOp(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s, "not json"); code != http.StatusBadRequest {
		t.Errorf("a malformed advance answered %d, want 400", code)
	}
}
