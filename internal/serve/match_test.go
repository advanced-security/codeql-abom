package serve

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The endpoint answers a set, and a set has no edges. It must refuse the predicates that shape a
// graph rather than accept and ignore them.
//
// Four predicates in this language have already been accepted and silently dropped — `origins`,
// `minReach`, `flags` and `ref` — and each time the real corpus, not a test, is what noticed. A
// caller asking for `kinds=workflow` and handed every kind has no way to tell.
func TestTheMatchEndpointRefusesPredicatesItCannotApply(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{"kinds=workflow", "origins=seed", "minReach=5", "ref=tag"} {
		w := httptest.NewRecorder()
		s.match(w, httptest.NewRequest("GET", "/api/match?rules=abom%2Fone&"+query, nil))
		if w.Code != 400 {
			t.Errorf("%s was accepted by the match endpoint with %d, want 400", query, w.Code)
		}
	}
}

// The finding predicates are exactly what it does apply.
func TestTheMatchEndpointAcceptsTheFindingPredicates(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{
		"rules=abom%2Fone", "notRules=abom%2Ftwo", "provider=CodeQL", "severity=error",
	} {
		w := httptest.NewRecorder()
		s.match(w, httptest.NewRequest("GET", "/api/match?"+query, nil))
		if w.Code != 200 {
			t.Errorf("%s was refused with %d, want 200: %s", query, w.Code, w.Body.String())
		}
	}
}

// A gate decides on the total, so an empty selector answering "everything" would fail every pull
// request rather than none.
func TestAMatchWithNoSelectorIsEmptyRatherThanEverything(t *testing.T) {
	s, _ := newServer(t)

	w := httptest.NewRecorder()
	s.match(w, httptest.NewRequest("GET", "/api/match", nil))
	if w.Code != 200 {
		t.Fatalf("an empty match was refused with %d", w.Code)
	}
	var got Match
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the match: %v", err)
	}
	if got.Total != 0 || got.Findings != 0 {
		t.Errorf("an empty selector matched %d nodes and %d findings; it must match none",
			got.Total, got.Findings)
	}
	if got.Nodes == nil {
		t.Error("Nodes is null rather than an empty list, which every client has to special-case")
	}
}

// The answer has to carry enough for a report to explain itself rather than only to fail.
func TestAMatchReportsHowManyFindingsAcrossHowManyNodes(t *testing.T) {
	s, st := newServer(t)
	var repoID int64
	var err error
	if repoID, err = st.UpsertRepository("octo-org/app", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(st, 0)
	if err := w.AddNode(store.Node{
		Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: repoID, Path: ".github/workflows/ci.yml",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := st.AddFindings([]store.Finding{
		{RuleID: "abom/one", RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 3},
		{RuleID: "abom/two", RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 4},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := st.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	rec := httptest.NewRecorder()
	s.match(rec, httptest.NewRequest("GET", "/api/match?rules=abom%2F%2A", nil))
	var got Match
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the match: %v", err)
	}
	if got.Total != 1 {
		t.Fatalf("matched %d nodes, want the one workflow: %s", got.Total, rec.Body.String())
	}
	if got.Findings != 2 {
		t.Errorf("Findings = %d, want both", got.Findings)
	}
	if len(got.Nodes[0].Rules) != 2 {
		t.Errorf("Rules = %v, want both named so a reader can disagree", got.Nodes[0].Rules)
	}
}
