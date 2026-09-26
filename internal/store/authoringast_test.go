package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// What PrintAST printed is what the agent read the fixtures as, and the session's scratch it was
// printed into is gone when the session ends; the store is the only place a reader finds it.
func TestAPrintedTreeIsKeptAndListedWithoutItsBody(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	tree := "#10000=UsesStep\n  #10001=ScalarValue actions/checkout@v4\n"
	id, err := s.RecordAuthoringAST(AuthoringAST{
		SessionID: session, QueryID: "acme/checkout-tag-pin", PrintedAt: "2026-09-23T12:00:00Z",
		Query: "PrintAST", Database: "test/CheckoutTagPin/CheckoutTagPin.testproj",
		SourceFiles: ".github/workflows/checkout.yml", TestDigest: "abc123", Body: tree,
		CLIVersion: testCLI,
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := s.AuthoringASTs(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].RowID != id || listed[0].RequestID != request ||
		listed[0].SessionID != session || listed[0].Size != len(tree) || listed[0].Body != "" ||
		listed[0].QueryID != "acme/checkout-tag-pin" || listed[0].TestDigest != "abc123" ||
		listed[0].CLIVersion != testCLI {
		t.Errorf("listed %+v", listed)
	}
	body, _ := json.Marshal(listed[0])
	if strings.Contains(string(body), `"body"`) {
		t.Errorf("a listing carries the tree: %s", body)
	}

	got, err := s.AuthoringAST(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != tree || got.Database != "test/CheckoutTagPin/CheckoutTagPin.testproj" ||
		got.SourceFiles != ".github/workflows/checkout.yml" || got.Truncated {
		t.Errorf("read back %+v", got)
	}
	if _, err := s.AuthoringAST(id + 1); !errors.Is(err, ErrNoAuthoringAST) {
		t.Errorf("a tree nobody printed: %v, want ErrNoAuthoringAST", err)
	}
	if none, err := s.AuthoringASTs(12345); err != nil || none == nil || len(none) != 0 {
		t.Errorf("no trees: %v %v, want an empty list", none, err)
	}
}

// A tree came from an agent's session or from ABOM at somebody's asking, and says which, because
// an agent steered one and nothing steered the other.
func TestAPrintedTreeNamesTheSessionOrThePersonThatPrintedIt(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	for name, a := range map[string]AuthoringAST{
		"neither a session nor a person": {QueryID: "q", PrintedAt: "t", Query: "PrintAST", Database: "d",
			Body: "b", CLIVersion: testCLI},
		"both a session and a person": {SessionID: session, PrintedBy: "operator", QueryID: "q", PrintedAt: "t",
			Query: "PrintAST", Database: "d", Body: "b", CLIVersion: testCLI},
		"no query":    {SessionID: session, PrintedAt: "t", Database: "d", Body: "b", CLIVersion: testCLI},
		"no database": {SessionID: session, PrintedAt: "t", Query: "PrintAST", Body: "b", CLIVersion: testCLI},
		"unknown session": {SessionID: session + 99, PrintedAt: "t", Query: "PrintAST", Database: "d",
			Body: "b", CLIVersion: testCLI},
		// The extractor that built the database shapes the tree, so a tree naming none could stand
		// in for any release.
		"no CodeQL": {SessionID: session, PrintedAt: "t", Query: "PrintAST", Database: "d", Body: "b"},
	} {
		if _, err := s.RecordAuthoringAST(a); err == nil {
			t.Errorf("%s: kept", name)
		}
	}
}

// A tree ABOM printed has no session and no request: it is listed by its query, and is as much
// evidence for the AST step as one a session printed.
func TestATreeABOMPrintedIsListedByItsQueryAndCountsForItsStep(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	base := AuthoringAST{QueryID: "acme/a", PrintedAt: "2026-09-24T12:00:00Z", Query: "PrintAST",
		Database: "test/A/A.testproj", SourceFiles: ".github/workflows/ci.yml", CLIVersion: testCLI}
	bySession, byABOM := base, base
	bySession.SessionID, bySession.TestDigest, bySession.Body = session, "old", "from the session"
	byABOM.PrintedBy, byABOM.TestDigest, byABOM.Body = "operator", "new", "from ABOM"
	if _, err := s.RecordAuthoringAST(bySession); err != nil {
		t.Fatal(err)
	}
	id, err := s.RecordAuthoringAST(byABOM)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := s.QueryASTs("acme/a")
	if err != nil || len(listed) != 2 || listed[0].RowID != id || listed[0].PrintedBy != "operator" ||
		listed[0].SessionID != 0 || listed[0].RequestID != 0 || listed[0].Body != "" ||
		listed[1].SessionID != session || listed[1].PrintedBy != "" {
		t.Errorf("the query's trees %+v %v", listed, err)
	}
	if newest, err := s.NewestASTs(); err != nil || newest["acme/a"].RowID != id {
		t.Errorf("newest %+v %v, want the tree ABOM printed", newest, err)
	}
	if got, err := s.AuthoringAST(id); err != nil || got.Body != "from ABOM" || got.PrintedBy != "operator" {
		t.Errorf("read back %+v %v", got, err)
	}
	if onRequest, err := s.AuthoringASTs(request); err != nil || len(onRequest) != 1 ||
		onRequest[0].SessionID != session {
		t.Errorf("the request's trees %+v %v, want only its session's", onRequest, err)
	}
	if none, err := s.QueryASTs("acme/none"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("no trees: %v %v, want an empty list", none, err)
	}
}

// The newest tree for each query is what says whether its AST step is current.
func TestTheNewestTreePrintedForEachQueryIsReadInOnePass(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	for _, a := range []AuthoringAST{
		{QueryID: "acme/a", TestDigest: "old", Body: "first"},
		{QueryID: "acme/a", TestDigest: "new", Body: "second"},
		{QueryID: "acme/b", TestDigest: "b1", Body: "only"},
	} {
		a.SessionID, a.PrintedAt, a.Query, a.Database = session, "2026-09-23T12:00:00Z", "PrintAST", "test/x"
		a.CLIVersion = testCLI
		if _, err := s.RecordAuthoringAST(a); err != nil {
			t.Fatal(err)
		}
	}
	newest, err := s.NewestASTs()
	if err != nil {
		t.Fatal(err)
	}
	if len(newest) != 2 || newest["acme/a"].TestDigest != "new" || newest["acme/b"].TestDigest != "b1" ||
		newest["acme/a"].Body != "" {
		t.Errorf("newest %+v", newest)
	}
}
