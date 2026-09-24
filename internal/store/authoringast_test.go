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
		listed[0].QueryID != "acme/checkout-tag-pin" || listed[0].TestDigest != "abc123" {
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

func TestAPrintedTreeBelongsToASessionAndSaysWhatItIsOf(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	for name, a := range map[string]AuthoringAST{
		"no session":  {QueryID: "q", PrintedAt: "t", Query: "PrintAST", Database: "d", Body: "b"},
		"no query":    {SessionID: session, PrintedAt: "t", Database: "d", Body: "b"},
		"no database": {SessionID: session, PrintedAt: "t", Query: "PrintAST", Body: "b"},
		"unknown session": {SessionID: session + 99, PrintedAt: "t", Query: "PrintAST", Database: "d",
			Body: "b"},
	} {
		if _, err := s.RecordAuthoringAST(a); err == nil {
			t.Errorf("%s: kept", name)
		}
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
