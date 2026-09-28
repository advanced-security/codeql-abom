package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The graph refuses a term neither it nor the browser applies.
func TestTheGraphRefusesATermNeitherSideApplies(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{"/api/graph/full", "/api/graph/delta?since=0"} {
		rec := httptest.NewRecorder()
		sep := "?"
		if path == "/api/graph/delta?since=0" {
			sep = "&"
		}
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(
			http.MethodGet, path+sep+"repositories=octo-org%2Fapp", nil))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d for a term nothing applies", path, rec.Code)
			continue
		}
		if body := rec.Body.String(); !contains(body, "repositories") {
			t.Errorf("%s refused without naming the term: %s", path, body)
		}
	}
}

// A term the browser applies is accepted and reported, so a caller that is not the Explorer knows
// it was handed work rather than having it done.
func TestTheGraphReportsWhatItHandedToTheClient(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/api/graph/full?rules=actions%2F*&kinds=action&scope=owned", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		ClientApplied []string `json:"clientApplied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the graph: %v", err)
	}
	want := map[string]bool{"kinds": true, "rules": true}
	if len(got.ClientApplied) != len(want) {
		t.Fatalf("clientApplied = %v, want kinds and rules", got.ClientApplied)
	}
	for _, term := range got.ClientApplied {
		if !want[term] {
			t.Errorf("clientApplied names %q, which the server applies itself", term)
		}
	}
}

// A predicate the server applies in full reports nothing, or every reader is told work is
// outstanding when none is.
func TestAServerAppliedPredicateReportsNoClientWork(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/api/graph/full?scope=owned&steps=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if contains(rec.Body.String(), "clientApplied") {
		t.Errorf("a fully applied predicate still reported client work: %s",
			rec.Body.String()[:min(200, len(rec.Body.String()))])
	}
}

// The graph narrows by `unanalyzed` and hands `findings` and `advisory` to the browser. It claimed
// all three, so `?flags=findings` answered the whole scope to a caller that was not the Explorer
// and reported no work left for it to do.
func TestTheGraphSaysWhichFlagsItLeftToTheClient(t *testing.T) {
	srv, _ := unitServer(t)

	for query, handed := range map[string]bool{
		"flags=findings&scope=owned":   true,
		"flags=advisory&scope=owned":   true,
		"flags=unanalyzed&scope=owned": false,
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/graph/full?"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", query, rec.Code, rec.Body.String())
		}
		var got struct {
			ClientApplied []string `json:"clientApplied"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding the graph: %v", err)
		}
		if said := len(got.ClientApplied) == 1 && got.ClientApplied[0] == "flags"; said != handed {
			t.Errorf("%s: clientApplied = %v, want flags named = %v", query, got.ClientApplied, handed)
		}
	}
}
