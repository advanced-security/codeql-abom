package serve

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func resolveDatabases(t *testing.T, s *Server, query string) (store.DatabaseSet, int) {
	t.Helper()
	w := httptest.NewRecorder()
	s.databases(w, httptest.NewRequest("GET", "/api/databases?"+query, nil))
	var got store.DatabaseSet
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding the database set: %v", err)
		}
	}
	return got, w.Code
}

// One language, or it is not the same scope. A separate syntax for choosing databases would make
// "what am I looking at" and "what am I about to run this against" two questions that look alike.
func TestTheDatabaseEndpointRefusesExactlyWhatTheMatchEndpointRefuses(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{"kinds=workflow", "origins=seed", "minReach=5", "ref=tag"} {
		matched := httptest.NewRecorder()
		s.match(matched, httptest.NewRequest("GET", "/api/match?rules=abom%2Fone&"+query, nil))
		_, code := resolveDatabases(t, s, "rules=abom%2Fone&"+query)
		if code != matched.Code {
			t.Errorf("%s answered %d resolving databases and %d matching nodes; one predicate "+
				"refused two ways is two policy engines", query, code, matched.Code)
		}
	}
}

// The finding predicates are what it does apply, and it has to apply the same ones.
func TestTheDatabaseEndpointAcceptsTheFindingPredicates(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{
		"rules=abom%2Fone", "notRules=abom%2Ftwo", "provider=CodeQL", "severity=error",
	} {
		if _, code := resolveDatabases(t, s, query); code != 200 {
			t.Errorf("%s was refused with %d, want 200", query, code)
		}
	}
}

// Refusing an unknown parameter proves a term was read, never that it was applied -- but a
// silently accepted one is how a scope comes to mean something it does not say.
func TestAnUnknownPredicateIsAnErrorRatherThanAWholeOrganization(t *testing.T) {
	s, _ := newServer(t)
	if _, code := resolveDatabases(t, s, "notAPredicate=1"); code != 400 {
		t.Errorf("an unknown predicate answered %d, want 400", code)
	}
}

// The answer has to carry its gaps to the reader, not only to the store: a set that arrives
// without them is the absent-is-not-zero failure arriving at the last hop.
func TestTheResolvedSetReachesTheReaderWithItsGapsAndItsMode(t *testing.T) {
	s, _ := newServer(t)

	set, code := resolveDatabases(t, s, "rules=actions%2F*")
	if code != 200 {
		t.Fatalf("resolving databases answered %d", code)
	}
	if set.Mode == "" {
		t.Error("the answer does not say which shape it resolved, so an empty one says nothing")
	}
	if set.Databases == nil {
		t.Error("databases marshaled as null rather than an empty list, which the client throws on")
	}
	if set.Gaps == nil {
		t.Error("gaps marshaled as null rather than an empty list")
	}
}
