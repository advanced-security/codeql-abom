package serve

import (
	"net/http"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// bothShapes seeds one repository whose workflow uses two actions: one observed by a regular
// analysis, one only by an extended analysis of the same repository's staged closure.
//
// Two distinct dependencies rather than one seen twice, because the question the endpoints answer
// is which rows a shape may count, and a row every shape sees cannot tell those apart.
func bothShapes(t *testing.T, s *store.Store) {
	t.Helper()

	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	extended, err := s.RecordQueryRun(store.QueryRun{
		QueryID: "actions/unpinned-tag", Repository: "octo-org/app", DBCommit: "cafe1234",
		AnalysisMode: store.AnalysisModeExtended,
	})
	if err != nil {
		t.Fatalf("recording the extended run: %v", err)
	}

	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
		{Key: "actions/staged", Kind: "action", Owner: "actions", Repo: "staged",
			IsExternal: true},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID,
			Path: ".github/workflows/ci.yml"},
		{Key: "step:app:0", Kind: "step", RepositoryID: repoID,
			ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	for _, e := range []store.Edge{
		{Key: "e-regular", SrcKey: "step:app:0", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "v4", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 10},
		{Key: "e-extended", SrcKey: "step:app:0", DstKey: "actions/staged",
			Kind: "step-uses-action", RefValue: "v1", RepositoryID: repoID,
			QueryRunID: extended, FilePath: "vendor/toolkit/action.yml", StartLine: 4},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DeriveContainment(); err != nil {
		t.Fatalf("DeriveContainment: %v", err)
	}

	// One defect per shape, on the dependency that shape observed, so a set answer naming the
	// wrong node is visible rather than merely a different count.
	if _, err := s.AddFindings([]store.Finding{
		{Provider: "codeql", RuleID: "actions/unpinned-tag", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 10, StartColumn: 9,
			Fingerprint: "fp-regular"},
		{Provider: "codeql", QueryRunID: extended, RuleID: "actions/unpinned-tag",
			RepositoryID: repoID, FilePath: "vendor/toolkit/action.yml", StartLine: 4,
			StartColumn: 1, Fingerprint: "fp-extended"},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	for fingerprint, node := range map[string]string{
		"fp-regular": "actions/checkout", "fp-extended": "actions/staged",
	} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_finding_node (finding_row_id, node_row_id)
			SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
			WHERE f.fingerprint = ? AND n.node_key = ?`, fingerprint, node); err != nil {
			t.Fatalf("attaching a finding: %v", err)
		}
	}
}

// The set path answers a predicate over nodes, so a mode term has to narrow the set it answers
// with rather than being accepted and ignored.
func TestTheSetPathAnswersEachShapeSeparately(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)

	for _, c := range []struct {
		query string
		want  string
	}{
		{"mode=regular", "actions/checkout"},
		{"mode=extended", "actions/staged"},
	} {
		got := decode[struct {
			Nodes []struct {
				Key string `json:"key"`
			} `json:"nodes"`
		}](t, get(t, srv, "/api/match?rules=actions%2Funpinned-tag&"+c.query))
		if len(got.Nodes) != 1 || got.Nodes[0].Key != c.want {
			t.Errorf("%s matched %+v, want only %s", c.query, got.Nodes, c.want)
		}
	}
}

// The graph applies the mode itself rather than handing it to the browser: a client cannot filter
// by a shape the payload never carried, and the edges a mode excludes are exactly the ones it
// never received.
func TestTheGraphPathDrawsOneShapeAtATime(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)

	for _, c := range []struct {
		query string
		want  string
	}{
		{"mode=regular", "actions/checkout"},
		{"mode=extended", "actions/staged"},
	} {
		got := decode[struct {
			Edges []struct {
				Target string   `json:"target"`
				Keys   []string `json:"keys"`
			} `json:"edges"`
			ClientApplied []string `json:"clientApplied"`
		}](t, get(t, srv, "/api/graph/full?steps=true&"+c.query))

		// Only the declared relationships: containment is drawn from node rows, is observed by
		// no run, and belongs to every shape.
		declared := []string{}
		for _, e := range got.Edges {
			if len(e.Keys) > 0 {
				declared = append(declared, e.Target)
			}
		}
		if len(declared) != 1 || declared[0] != c.want {
			t.Errorf("%s drew %v, want the one declared edge to %s", c.query, declared, c.want)
		}
		for _, term := range got.ClientApplied {
			if term == "mode" {
				t.Errorf("%s was handed to the browser, which never received the other shape",
					c.query)
			}
		}
	}
}

// A listing that declares support has to answer differently for each shape, or the term is
// decoration on a page that counted everything anyway.
func TestEveryListingThatDeclaresModeAnswersIt(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)

	for _, c := range []struct {
		path string
		want string
	}{
		{"/api/actions?rules=actions%2Funpinned-tag&mode=regular", "actions/checkout"},
		{"/api/actions?rules=actions%2Funpinned-tag&mode=extended", "actions/staged"},
		{"/api/relationships?mode=regular", "actions/checkout"},
		{"/api/relationships?mode=extended", "actions/staged"},
	} {
		body := get(t, srv, c.path)
		if body.Code != http.StatusOK {
			t.Errorf("%s answered %d: %s", c.path, body.Code, body.Body.String())
			continue
		}
		text := body.Body.String()
		if !contains(text, c.want) {
			t.Errorf("%s did not name %s: %s", c.path, c.want, text)
		}
		other := "actions/staged"
		if c.want == other {
			other = "actions/checkout"
		}
		if contains(text, other) {
			t.Errorf("%s also returned %s, so the shapes are interchangeable", c.path, other)
		}
	}
}

// A route that cannot narrow by shape must refuse the term rather than answer a question it did
// not apply. A repository listing counts a repository's whole history of analysis, and a reader
// following a link carrying `mode=extended` would be shown the regular totals under that name.
func TestRoutesThatCannotEnforceModeRefuseIt(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)

	for _, path := range []string{
		"/api/repositories?mode=extended",
		"/api/workflows?mode=extended",
		"/api/jobs?mode=extended",
		"/api/advisories?mode=extended",
	} {
		rec := get(t, srv, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d for a term it does not apply", path, rec.Code)
			continue
		}
		if !contains(rec.Body.String(), "mode") {
			t.Errorf("%s refused without naming the term: %s", path, rec.Body.String())
		}
	}
}

// An unspelled mode is `regular`, so every reader who never heard of the term keeps the totals
// they had. The alternative — an unfiltered default spanning both shapes — is the silent
// interchange the whole column exists to prevent.
func TestAnAbsentModeAnswersTheRegularShape(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)

	got := decode[struct {
		Nodes []struct {
			Key string `json:"key"`
		} `json:"nodes"`
	}](t, get(t, srv, "/api/match?rules=actions%2Funpinned-tag"))
	if len(got.Nodes) != 1 || got.Nodes[0].Key != "actions/checkout" {
		t.Errorf("an unspelled mode matched %+v, want the regular shape alone", got.Nodes)
	}
}
