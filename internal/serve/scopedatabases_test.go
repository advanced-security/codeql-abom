package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// What is counted as held before Run is what this CodeQL would run against: a database another
// CodeQL built is rebuilt, so the preview and the run both say so and neither names the old one.
func TestTheSetARunCoversIsResolvedAgainstTheCodeQLRunningNow(t *testing.T) {
	s, st := newServer(t)
	s.Tester = &fakeTester{}
	if _, err := st.RetainDatabase(store.RetainedDatabase{
		DatabaseIdentity: store.DatabaseIdentity{
			Repository: "octo-org/app", Commit: "sha-app", SourceFilter: "f0",
			Mode: store.AnalysisModeRegular,
		},
		Path: "/cache/octo-org/app/sha-app/db", Bytes: 8192, CLIVersion: "2.27.0",
	}); err != nil {
		t.Fatalf("RetainDatabase: %v", err)
	}

	set, code := resolveDatabases(t, s, "repositories=octo-org%2Fapp")
	if code != 200 || len(set.Databases) != 1 {
		t.Fatalf("resolving answered %d with %+v", code, set)
	}
	if d := set.Databases[0]; d.Status != store.DatabaseBuildable ||
		!strings.Contains(d.Reason, "2.27.0") || d.Path != "" {
		t.Errorf("the preview counts a database %s built as held under %s: %+v",
			"2.27.0", fakeCLI, d)
	}

	w := httptest.NewRecorder()
	s.startRun(w, httptest.NewRequest("POST", "/api/runs?repositories=octo-org%2Fapp",
		bytes.NewBufferString(oneQuery)))
	if w.Code != 200 {
		t.Fatalf("starting the run answered %d: %s", w.Code, w.Body.String())
	}
	var run store.AnalysisRun
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("decoding the run: %v", err)
	}
	if len(run.Members) != 1 || run.Members[0].DatabasePath != "" ||
		!strings.Contains(run.Members[0].Detail, "2.27.0") {
		t.Errorf("the run was not recorded over what the preview showed: %+v", run.Members)
	}
}

// A CodeQL that cannot be asked cannot be compared against, and the preview says so rather than
// counting what it cannot vouch for.
func TestAResolutionRefusesWhenTheCodeQLRunningCannotBeRead(t *testing.T) {
	s, _ := newServer(t)
	s.Tester = &fakeTester{unreadable: errors.New("codeql: not found")}

	if _, code := resolveDatabases(t, s, "repositories=octo-org%2Fapp"); code == 200 {
		t.Error("the preview answered with a count it could not check against any CodeQL")
	}
}
