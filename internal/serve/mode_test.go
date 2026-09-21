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
		{"rules=actions%2Funpinned-tag&mode=regular", "actions/checkout"},
		{"rules=actions%2Funpinned-tag&mode=extended", "actions/staged"},
		{"mode=regular", "actions/checkout"},
		{"mode=extended", "actions/staged"},
	} {
		got := decode[struct {
			Nodes []struct {
				Key string `json:"key"`
			} `json:"nodes"`
		}](t, get(t, srv, "/api/match?"+c.query))
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
			Nodes []struct {
				Key  string `json:"key"`
				Kind string `json:"kind"`
			} `json:"nodes"`
			Edges []struct {
				Target string   `json:"target"`
				Keys   []string `json:"keys"`
			} `json:"edges"`
			ClientApplied []string `json:"clientApplied"`
		}](t, get(t, srv, "/api/graph/full?steps=true&"+c.query))

		actions := []string{}
		for _, node := range got.Nodes {
			if node.Kind == "action" {
				actions = append(actions, node.Key)
			}
		}
		if len(actions) != 1 || actions[0] != c.want {
			t.Errorf("%s drew action nodes %v, want only %s", c.query, actions, c.want)
		}

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
		{"/api/actions?mode=regular", "actions/checkout"},
		{"/api/actions?mode=extended", "actions/staged"},
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

func TestTheNeighborhoodRefusesUnknownAnalysisShapes(t *testing.T) {
	srv, database := unitServer(t)
	bothShapes(t, database)
	for _, mode := range []string{"closure", "mixed", ""} {
		response := get(t, srv, "/api/graph?focus=actions/checkout&mode="+mode)
		if response.Code != http.StatusBadRequest || !contains(response.Body.String(), "mode") {
			t.Errorf("mode=%q returned %d: %s", mode, response.Code, response.Body.String())
		}
	}
}

func TestAnUnfocusedGraphStillUsesTheRequestedAnalysisShape(t *testing.T) {
	_, database := unitServer(t)
	bothShapes(t, database)
	for _, shape := range []struct{ mode, target string }{
		{"regular", "actions/checkout"}, {"extended", "actions/staged"},
	} {
		graph, err := database.Subgraph(store.SubgraphOptions{Mode: shape.mode})
		if err != nil {
			t.Fatal(err)
		}
		if len(graph.Edges) != 1 || graph.Edges[0].Target != shape.target {
			t.Errorf("%s overview edges = %+v, want only %s", shape.mode, graph.Edges, shape.target)
		}
	}
}

func TestListingCountsFlagsAndReferencesUseTheirAnalysisShape(t *testing.T) {
	srv, s := unitServer(t)
	bothShapes(t, s)
	if _, err := s.DB().Exec(`
		INSERT OR IGNORE INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
		WHERE n.node_key = 'step:app:0'
		   OR (f.fingerprint = 'fp-extended' AND n.node_key = 'actions/checkout')`); err != nil {
		t.Fatal(err)
	}

	for _, shape := range []struct{ mode, action string }{
		{"regular", "checkout"}, {"extended", "staged"},
	} {
		t.Run(shape.mode, func(t *testing.T) {
			page := decode[store.InventoryPage](t, get(t, srv,
				"/api/actions?mode="+shape.mode+"&q="+shape.action))
			if len(page.Items) != 1 || page.Total != 1 {
				t.Fatalf("inventory = %+v, want one action", page)
			}
			if page.Items[0].Findings != 1 || len(page.Items[0].Refs) != 1 {
				t.Errorf("inventory = %+v, want one finding and one reference in %s",
					page.Items[0], shape.mode)
			}
			relationships := decode[store.RelationshipsPage](t, get(t, srv,
				"/api/relationships?mode="+shape.mode+"&sort=findings"))
			if len(relationships.Items) != 1 || relationships.Total != 1 {
				t.Fatalf("relationships = %+v, want one relationship", relationships)
			}
			if relationships.Items[0].Findings != 1 || len(relationships.Items[0].Rules) != 1 {
				t.Errorf("relationship = %+v, want one finding and its rule in %s",
					relationships.Items[0], shape.mode)
			}
		})
	}

	if _, err := s.DB().Exec(`DELETE FROM abom_finding_node
		WHERE finding_row_id IN (SELECT row_id FROM abom_finding WHERE fingerprint = 'fp-regular')
		AND node_row_id IN (SELECT row_id FROM abom_node WHERE node_key = 'actions/checkout')`); err != nil {
		t.Fatal(err)
	}
	page := decode[store.InventoryPage](t, get(t, srv,
		"/api/actions?mode=regular&q=checkout&flags=findings"))
	if page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("an extended-only finding satisfied the regular findings flag: %+v", page)
	}
}

func TestSharedEdgesPreserveRegularEvidenceAndRefuseConflictingShapes(t *testing.T) {
	for _, reference := range []string{"v4", "v5"} {
		t.Run(reference, func(t *testing.T) {
			srv, database := unitServer(t)
			bothShapes(t, database)
			var runID int64
			if err := database.DB().QueryRow(`SELECT row_id FROM abom_query_run
				WHERE analysis_mode = 'extended'`).Scan(&runID); err != nil {
				t.Fatal(err)
			}
			writer := store.NewWriter(database, 0)
			for _, node := range []store.Node{
				{Key: "step:app:0", Kind: "step"},
				{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout"},
			} {
				if err := writer.AddNode(node); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.AddEdge(store.Edge{
				Key: "e-regular", SrcKey: "step:app:0", DstKey: "actions/checkout",
				Kind: "step-uses-action", RefValue: reference, QueryRunID: runID,
				FilePath: ".github/workflows/ci.yml", StartLine: 10,
			}); err != nil {
				t.Fatal(err)
			}
			err := writer.Flush()
			if reference == "v4" && err != nil {
				t.Fatal(err)
			}
			if reference != "v4" && (err == nil || !contains(err.Error(), "analysis mode")) {
				t.Errorf("a conflicting shape must be refused by name, got %v", err)
			}
			regular := decode[store.RelationshipsPage](t, get(t, srv, "/api/relationships"))
			if len(regular.Items) != 1 || regular.Total != 1 || regular.Items[0].Ref != "v4" {
				t.Errorf("an extended observation changed regular evidence: %+v", regular)
			}
			if reference == "v4" {
				extended := decode[store.RelationshipsPage](t, get(t, srv, "/api/relationships?mode=extended"))
				if extended.Total != 2 {
					t.Errorf("the extended shape lost the shared edge: %+v", extended)
				}
			}
		})
	}
}

func TestAnExtendedSightingKeepsAnUntracedRegularFinding(t *testing.T) {
	srv, database := unitServer(t)
	bothShapes(t, database)
	var runID, repositoryID int64
	if err := database.DB().QueryRow(`SELECT row_id FROM abom_query_run
		WHERE analysis_mode = 'extended'`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRow(`SELECT row_id FROM repository
		WHERE repository_full_name = 'octo-org/app'`).Scan(&repositoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddFindings([]store.Finding{{
		Provider: "codeql", RuleID: "actions/unpinned-tag", RepositoryID: repositoryID,
		FilePath: ".github/workflows/ci.yml", StartLine: 10, StartColumn: 9,
		Fingerprint: "fp-regular", QueryRunID: runID,
	}}); err != nil {
		t.Fatal(err)
	}
	regular := decode[store.FindingMatch](t, get(t, srv, "/api/match?mode=regular"))
	if regular.Findings != 1 || len(regular.Nodes) != 1 || regular.Nodes[0].Key != "actions/checkout" {
		t.Errorf("an extended sighting removed existing regular evidence: %+v", regular)
	}
	var identities int
	if err := database.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if identities != 2 {
		t.Errorf("re-observation changed the number of defects to %d, want 2", identities)
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
