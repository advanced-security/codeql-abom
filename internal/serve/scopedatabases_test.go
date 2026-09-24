package serve

import (
	"encoding/json"
	"net/http"
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

// The preview is the run's own resolution, so a term a run refuses is refused here too. Refusing
// less would show a reader a set the run then declines to cover.
func TestTheDatabaseEndpointRefusesWhatARunRefuses(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{
		"kinds=workflow", "origins=seed", "minReach=5", "ref=tag", "flags=findings",
	} {
		started, _ := startRefusal(t, s, "rules=abom%2Fone&"+query)
		_, code := resolveDatabases(t, s, "rules=abom%2Fone&"+query)
		if code != http.StatusBadRequest || code != started {
			t.Errorf("%s answered %d previewing and %d starting a run, want both refused",
				query, code, started)
		}
	}
}

// Every term that chooses repositories is applied: the unit's scope, names, findings and shape.
func TestTheDatabaseEndpointAcceptsEveryTermThatChoosesRepositories(t *testing.T) {
	s, _ := newServer(t)

	for _, query := range []string{
		"rules=abom%2Fone", "notRules=abom%2Ftwo", "provider=CodeQL", "severity=error",
		"scope=owned", "q=app", "properties=business_id%3A3", "repositories=octo-org%2Fapp",
		"mode=regular",
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
