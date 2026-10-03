package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakeDeclared reads declarations as the sweep does: what it was told a version declares is
// recorded, and every other version it was asked about comes back with a reason.
type fakeDeclared struct {
	mu       sync.Mutex
	store    *store.Store
	declares map[string]map[string]string
	cannot   map[string]string
	// failOnce is what the first sweep fails with, as one does where CodeQL cannot run.
	failOnce error
	asked    [][]string
}

func (f *fakeDeclared) read(_ context.Context, unread []store.CatalogQuery) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(unread))
	for _, q := range unread {
		keys = append(keys, q.QueryID+"\x00"+q.QueryVersion)
	}
	slices.Sort(keys)
	f.asked = append(f.asked, keys)
	if err := f.failOnce; err != nil {
		f.failOnce = nil
		return nil, err
	}
	out := map[string]string{}
	var read []store.QueryMetadata
	for _, q := range unread {
		key := q.QueryID + "\x00" + q.QueryVersion
		if pairs, ok := f.declares[key]; ok {
			read = append(read, store.QueryMetadata{QueryID: q.QueryID, QueryVersion: q.QueryVersion, Pairs: pairs})
			continue
		}
		out[key] = f.cannot[key]
	}
	return out, f.store.RecordQueryMetadata(read)
}

func (f *fakeDeclared) sweeps() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func declaredServer(t *testing.T, runs ...store.QueryRun) (*Server, *fakeDeclared) {
	t.Helper()
	s, st := newServer(t)
	for _, run := range runs {
		run.AnalysisMode, run.Repository, run.DBCommit = store.AnalysisModeRegular, "octo/app", "sha-one"
		if _, err := st.RecordQueryRun(run); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeDeclared{store: st, declares: map[string]map[string]string{}, cannot: map[string]string{}}
	s.ReadDeclarations = fake.read
	return s, fake
}

// catalogRows reads the catalog as the interface does, by query id and version, once whatever
// sweep it set off has finished.
func catalogRows(t *testing.T, s *Server) map[string]catalogRow {
	t.Helper()
	w := httptest.NewRecorder()
	s.queries(w, httptest.NewRequest("GET", "/api/queries", nil))
	if w.Code != 200 {
		t.Fatalf("/api/queries answered %d: %s", w.Code, w.Body.String())
	}
	// Held while the answer is read, so the sweep the listing set off has finished first.
	s.declarations.sweeping.Lock()
	defer s.declarations.sweeping.Unlock()
	var got struct {
		Queries []catalogRow `json:"queries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	out := map[string]catalogRow{}
	for _, q := range got.Queries {
		out[q.QueryID+"\x00"+q.QueryVersion] = q
	}
	return out
}

const unpinned = "actions/unpinned-tag\x00u1"

// Nobody should have to ask for a version's declaration: the catalog listing one nothing has read
// is what reads it, and says so in the meantime rather than reading as a query declaring nothing.
func TestTheCatalogReadsWhatAVersionDeclaresWithoutAnybodyAsking(t *testing.T) {
	s, fake := declaredServer(t, store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35"})
	fake.declares[unpinned] = map[string]string{"id": "actions/unpinned-tag", "kind": "problem",
		"security-severity": "5.0"}

	first := catalogRows(t, s)[unpinned]
	if first.Metadata != nil || first.MetadataUnread != declarationReading {
		t.Errorf("before it is read the row says %q with %v, want %q", first.MetadataUnread, first.Metadata,
			declarationReading)
	}
	second := catalogRows(t, s)[unpinned]
	if second.Metadata["security-severity"] != "5.0" || second.MetadataUnread != "" {
		t.Errorf("after the sweep the row declares %v and says %q, want its declaration and nothing else",
			second.Metadata, second.MetadataUnread)
	}
	if got := fake.sweeps(); len(got) != 1 || !slices.Equal(got[0], []string{unpinned}) {
		t.Errorf("swept %q, want one sweep asking for %q", got, unpinned)
	}
}

// A version is its content, so one no file on disk is will not become one by being asked again:
// it is named once, and a listing every few seconds does not send the CLI looking for it again.
func TestAVersionWhoseFileIsGoneIsNamedOnceAndNotLookedForAgain(t *testing.T) {
	s, fake := declaredServer(t, store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35"})
	const why = "Declared metadata cannot be read: no file of codeql/actions-queries 0.6.35 on disk is this version."
	fake.cannot[unpinned] = why

	catalogRows(t, s)
	if got := catalogRows(t, s)[unpinned].MetadataUnread; got != why {
		t.Errorf("the row says %q, want the sweep's reason %q", got, why)
	}
	catalogRows(t, s)
	if got := fake.sweeps(); len(got) != 1 {
		t.Errorf("three listings swept %d times, want once: %q", len(got), got)
	}
}

// Which file a run read was never recorded for the oldest rows, so nothing on disk can be matched
// to them and asking would send the CLI after something it cannot find. That is permanent, and
// "not read yet" would promise a read that will never come.
func TestARunWithNoRecordedVersionSaysWhatItDeclaredCannotBeKnown(t *testing.T) {
	s, fake := declaredServer(t, store.QueryRun{QueryID: "abom/edges", QueryVersion: "",
		Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.26.4"})

	row := catalogRows(t, s)["abom/edges\x00"]
	if row.MetadataUnread != declarationUnversioned {
		t.Errorf("an unversioned row says %q, want %q", row.MetadataUnread, declarationUnversioned)
	}
	if got := fake.sweeps(); len(got) != 0 {
		t.Errorf("swept for a version nobody recorded: %q", got)
	}
}

// A sweep that could not run has learned nothing about the disk: the rows say why, and the next
// listing tries again rather than leaving them unread until a restart.
func TestASweepThatCouldNotRunSaysWhyAndIsTriedAgain(t *testing.T) {
	s, fake := declaredServer(t, store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35"})
	fake.failOnce = errors.New("reading declared metadata needs CodeQL: codeql: executable file not found")
	fake.declares[unpinned] = map[string]string{"id": "actions/unpinned-tag", "security-severity": "5.0"}

	catalogRows(t, s)
	if got := catalogRows(t, s)[unpinned].MetadataUnread; got != declarationReading+" The last attempt failed: "+
		"reading declared metadata needs CodeQL: codeql: executable file not found" {
		t.Errorf("after a failed sweep the row says %q, want the reading notice and why the last attempt failed", got)
	}
	if got := catalogRows(t, s)[unpinned].Metadata["security-severity"]; got != "5.0" {
		t.Errorf("the retried sweep left the row declaring %q, want 5.0", got)
	}
}

// A server with no CodeQL has nothing to read a declaration with, which is not the same as one
// that has not got to it yet.
func TestWithNothingToReadWithAnUnreadVersionSaysSo(t *testing.T) {
	s, _ := declaredServer(t, store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35"})
	s.ReadDeclarations = nil

	if got := catalogRows(t, s)[unpinned].MetadataUnread; got != declarationNoReader {
		t.Errorf("with no reader the row says %q, want %q", got, declarationNoReader)
	}
}

// A corpus written before declarations were recorded, or listed by a CodeQL since replaced, is read
// as the server starts: nobody has to open the catalog, and nobody has to run anything.
func TestTheServerReadsEveryUnreadVersionAsItStarts(t *testing.T) {
	s, fake := declaredServer(t,
		store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
			Pack: "codeql/actions-queries", PackVersion: "0.6.35"},
		store.QueryRun{QueryID: "abom/edges", QueryVersion: "",
			Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.26.4"})
	root := queryWorkspace(t, []string{"A"})
	s.QueryDir = root
	working := "acme/a\x00" + versionOf(t, root, "A")
	fake.declares[unpinned] = map[string]string{"id": "actions/unpinned-tag", "security-severity": "5.0"}
	fake.declares[working] = map[string]string{"id": "acme/a", "security-severity": "7.0"}

	s.ReadUnreadDeclarations(context.Background())

	if got, want := fake.sweeps(), [][]string{{working, unpinned}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("starting swept %q, want %q: every versioned row without a declaration, working copy included",
			got, want)
	}
	rows := catalogRows(t, s)
	for _, key := range []string{unpinned, working} {
		if rows[key].Metadata == nil {
			t.Errorf("%q is still unread after the server started", key)
		}
	}
	if got := fake.sweeps(); len(got) != 1 {
		t.Errorf("the first listing after starting swept again: %q", got)
	}
}
