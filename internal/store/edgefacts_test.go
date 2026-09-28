package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A 40-character hex reference is the one style a reader can check without asking anybody.
func TestACommitReferenceReadsAsACommit(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "pinned", "ref_style"); got != EdgeRefCommit {
		t.Errorf("ref_style = %q, want %q", got, EdgeRefCommit)
	}
}

// `v4` is how almost every workflow names a release, and calling it a branch would report the
// whole corpus as floating on a moving branch.
func TestAVersionReferenceReadsAsATag(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "floating", "ref_style"); got != EdgeRefTag {
		t.Errorf("ref_style = %q, want %q", got, EdgeRefTag)
	}
}

func TestAnExplicitRefsTagsReferenceReadsAsATag(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "qualified", "ref_style"); got != EdgeRefTag {
		t.Errorf("ref_style = %q, want %q", got, EdgeRefTag)
	}
}

// The branch a repository builds from is a measurement, so a reference naming it is a branch
// rather than a guess about what the word looks like.
func TestAReferenceNamingARecordedDefaultBranchReadsAsABranch(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "branchy", "ref_style"); got != EdgeRefBranch {
		t.Errorf("ref_style = %q, want %q", got, EdgeRefBranch)
	}
}

// `wip` could be a tag or a branch and nothing on record says which. Unknown is not a class, and
// naming it either way would report a style nobody observed.
func TestAReferenceNothingCanClassifyStaysUnknown(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	for _, key := range []string{"mystery", "local"} {
		if got := factOf(t, s, key, "ref_style"); got != "" {
			t.Errorf("ref_style of %s = %q, want it unset", key, got)
		}
	}
}

// `ref_style` reads the reference, not only the target. A line rewritten from `@v4` to a commit
// on the same action keeps its target and changes its style, so preserving the old answer left the
// relationships list reporting a tag until some later aggregate rebuild happened to run.
func TestARewrittenReferenceForgetsTheStyleOfTheOldOne(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)
	if got := factOf(t, s, "floating", "ref_style"); got != EdgeRefTag {
		t.Fatalf("ref_style = %q, want %q before the reference moves", got, EdgeRefTag)
	}

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "step:octo-org/app:ci.yml::1", Kind: "step"},
		{Key: "actions/setup-node", Kind: "action", Owner: "actions", Repo: "setup-node",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "floating", SrcKey: "step:octo-org/app:ci.yml::1", DstKey: "actions/setup-node",
		Kind: "step-uses-action", RefValue: "0123456789abcdef0123456789abcdef01234567",
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := factOf(t, s, "floating", "ref_style"); got != "" {
		t.Errorf("ref_style = %q, want it unset: the reference it classified is gone", got)
	}
	derive(t, s)
	if got := factOf(t, s, "floating", "ref_style"); got != EdgeRefCommit {
		t.Errorf("ref_style = %q, want %q: the line now names a commit", got, EdgeRefCommit)
	}
}

// Settling to zero is what lets the derivation run after every ingest without a second mechanism
// deciding when it is due.
func TestDerivingEdgeFactsTwiceChangesNothing(t *testing.T) {
	s := edgeFactsFixture(t)
	if first := derive(t, s); first == 0 {
		t.Fatal("the first pass derived nothing, so the second proves nothing")
	}

	if again := derive(t, s); again != 0 {
		t.Errorf("the second pass changed %d edges, want none", again)
	}
}

func TestAnEdgeWhoseTargetCarriesAFindingIsOnARouteToOne(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "intoflawed", "finding_route"); got != "1" {
		t.Errorf("finding_route = %q, want 1: the target carries a finding", got)
	}
}

// The walk is the point: a repository's own reference is clean and what it pulls in is not, and
// only following the relationship says so.
func TestAnEdgeReachingAFindingThroughAnotherEdgeIsOnARouteToOne(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "intovendor", "finding_route"); got != "1" {
		t.Errorf("finding_route = %q, want 1: the target's own dependency carries a finding", got)
	}
}

// False is a measurement here, because the walk covered this edge and found nothing. Leaving it
// unset would make "nothing was found" indistinguishable from "nobody looked".
func TestAnEdgeThatReachesNoFindingSaysSoRatherThanStayingUnknown(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "pinned", "finding_route"); got != "0" {
		t.Errorf("finding_route = %q, want 0", got)
	}
}

// Read from the stored exposure path rather than walked again, so this fact and the Remediation
// page cannot disagree about what is exposed.
func TestAnEdgeOnAStoredAdvisoryRouteIsMarked(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "intovendor", "advisory_route"); got != "1" {
		t.Errorf("advisory_route = %q, want 1: the route to the advisory runs through it", got)
	}
	if got := factOf(t, s, "pinned", "advisory_route"); got != "0" {
		t.Errorf("advisory_route = %q, want 0: no stored route names it", got)
	}
}

// hops of 1 means the repository names the dependency itself and can fix it alone.
func TestADirectReferenceFromAnOwnedRepositoryIsOneHop(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "intovendor", "owned_depth"); got != "1" {
		t.Errorf("owned_depth = %q, want 1", got)
	}
}

// Direct and transitive are different work, and reading the depth is what stops the interface
// walking the graph again to tell them apart.
func TestAReferenceInsideADependencyIsTwoHops(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "vendordeep", "owned_depth"); got != "2" {
		t.Errorf("owned_depth = %q, want 2: it is declared inside a dependency", got)
	}
}

// An edge in a repository nobody declared is at no depth from an owned one. Zero would read as
// "the operator declares this", which is the claim ownership exists to make deliberately.
func TestAnEdgeNothingOwnedReachesHasNoDepth(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	if got := factOf(t, s, "stranded", "owned_depth"); got != "" {
		t.Errorf("owned_depth = %q, want it unset", got)
	}
}

// NULL is not computed, which is what an edge ingested since the last aggregate pass is.
func TestAnEdgeIngestedSinceTheLastPassCarriesNoFacts(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow"},
		{Key: "late/arrival", Kind: "action", Owner: "late", Repo: "arrival", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "late", SrcKey: "octo-org/app/.github/workflows/ci.yml", DstKey: "late/arrival",
		Kind: "step-uses-action", RefValue: "v1", FilePath: ".github/workflows/ci.yml",
		StartLine: 9,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for _, column := range []string{"ref_style", "advisory_route", "finding_route", "owned_depth"} {
		if got := factOf(t, s, "late", column); got != "" {
			t.Errorf("%s = %q on an edge nothing has derived yet, want it unset", column, got)
		}
	}
}

// Re-analysis can move an edge to a different target, which invalidates facts derived from the
// previous target.
func TestARepointedEdgeLosesTheFactsDerivedFromItsOldTarget(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)
	if got := factOf(t, s, "intoflawed", "finding_route"); got != "1" {
		t.Fatalf("finding_route = %q, want 1 before the target moves", got)
	}

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow"},
		{Key: "clean/action", Kind: "action", Owner: "clean", Repo: "action", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "intoflawed", SrcKey: "octo-org/app/.github/workflows/ci.yml",
		DstKey: "clean/action", Kind: "step-uses-action", RefValue: "v1",
		FilePath: ".github/workflows/ci.yml", StartLine: 4,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := factOf(t, s, "intoflawed", "finding_route"); got != "" {
		t.Errorf("finding_route = %q, want it unset: the edge points somewhere else now", got)
	}
	derive(t, s)
	if got := factOf(t, s, "intoflawed", "finding_route"); got != "0" {
		t.Errorf("finding_route = %q, want 0 after deriving against the new target", got)
	}
}

func derive(t *testing.T, s *Store) int {
	t.Helper()
	n, err := s.DeriveEdgeFacts()
	if err != nil {
		t.Fatalf("DeriveEdgeFacts: %v", err)
	}
	return n
}

// factOf reads one derived column, with the empty string standing for NULL.
func factOf(t *testing.T, s *Store, edgeKey, column string) string {
	t.Helper()
	var v sql.NullString
	if err := s.DB().QueryRow(
		`SELECT `+column+` FROM abom_edge WHERE edge_key = ?`, edgeKey).Scan(&v); err != nil {
		t.Fatalf("reading %s of %s: %v", column, edgeKey, err)
	}
	return v.String
}

// edgeFactsFixture holds one edge of every shape the derivation has to tell apart: a repository
// the operator declared, a dependency of it that declares its own, and a repository nothing owned
// reaches at all.
func edgeFactsFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	declares(t, s, "octo-org")

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if _, err := s.UpsertRepository("octo-org/lib", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.SetDefaultBranch("octo-org/lib", "trunk"); err != nil {
		t.Fatalf("SetDefaultBranch: %v", err)
	}
	stranger, err := s.UpsertRepository("stranger/thing", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := NewWriter(s, 0)
	add := func(n Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	workflow := "octo-org/app/.github/workflows/ci.yml"
	add(Node{Key: workflow, Kind: "workflow", RepositoryID: app})
	for _, step := range []string{"0", "1", "2", "3", "4", "5", "6", "7"} {
		add(Node{Key: "step:octo-org/app:ci.yml::" + step, Kind: "step", RepositoryID: app,
			ContainerKey: workflow})
	}
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})
	add(Node{Key: "actions/setup-node", Kind: "action", Owner: "actions", Repo: "setup-node",
		IsExternal: true})
	add(Node{Key: "octo-org/lib", Kind: "composite_action", Owner: "octo-org", Repo: "lib",
		IsExternal: true})
	add(Node{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache",
		IsExternal: true})
	add(Node{Key: "mystery/action", Kind: "action", Owner: "mystery", Repo: "action",
		IsExternal: true})
	add(Node{Key: "local-thing", Kind: "composite_action"})
	add(Node{Key: "flawed/action", Kind: "action", Owner: "flawed", Repo: "action",
		IsExternal: true})
	add(Node{Key: "vendor/tool", Kind: "composite_action", Owner: "vendor", Repo: "tool",
		IsExternal: true})
	// A step of the dependency's own definition, which is what makes its references transitive.
	add(Node{Key: "step:vendor/tool:action.yml::0", Kind: "step", ContainerKey: "vendor/tool"})
	add(Node{Key: "deep/dependency", Kind: "action", Owner: "deep", Repo: "dependency",
		IsExternal: true})
	// A repository nothing the operator declared reaches.
	add(Node{Key: "stranger/thing/.github/workflows/build.yml", Kind: "workflow",
		RepositoryID: stranger})
	add(Node{Key: "stranger/dep", Kind: "action", Owner: "stranger", Repo: "dep",
		IsExternal: true})

	for _, e := range []Edge{
		{Key: "pinned", SrcKey: "step:octo-org/app:ci.yml::0", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
			RepositoryID: app},
		{Key: "floating", SrcKey: "step:octo-org/app:ci.yml::1", DstKey: "actions/setup-node",
			Kind: "step-uses-action", RefValue: "v4", RepositoryID: app},
		{Key: "branchy", SrcKey: "step:octo-org/app:ci.yml::2", DstKey: "octo-org/lib",
			Kind: "step-uses-action", RefValue: "trunk", RepositoryID: app},
		{Key: "qualified", SrcKey: "step:octo-org/app:ci.yml::3", DstKey: "actions/cache",
			Kind: "step-uses-action", RefValue: "refs/tags/v2", RepositoryID: app},
		{Key: "mystery", SrcKey: "step:octo-org/app:ci.yml::4", DstKey: "mystery/action",
			Kind: "step-uses-action", RefValue: "wip", RepositoryID: app},
		{Key: "local", SrcKey: "step:octo-org/app:ci.yml::5", DstKey: "local-thing",
			Kind: "step-uses-action", RepositoryID: app},
		{Key: "intoflawed", SrcKey: "step:octo-org/app:ci.yml::6", DstKey: "flawed/action",
			Kind: "step-uses-action", RefValue: "v1", RepositoryID: app},
		{Key: "intovendor", SrcKey: "step:octo-org/app:ci.yml::7", DstKey: "vendor/tool",
			Kind: "step-uses-action", RefValue: "v2", RepositoryID: app},
		{Key: "vendordeep", SrcKey: "step:vendor/tool:action.yml::0", DstKey: "deep/dependency",
			Kind: "composite-step-uses-action", RefValue: "v3"},
		{Key: "stranded", SrcKey: "stranger/thing/.github/workflows/build.yml",
			DstKey: "stranger/dep", Kind: "step-uses-action", RefValue: "v1",
			RepositoryID: stranger},
	} {
		e.FilePath = ".github/workflows/ci.yml"
		e.StartLine = 1
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	findingOn(t, s, app, "flawed/action")
	findingOn(t, s, app, "deep/dependency")
	if _, err := s.DB().Exec(`
		INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
		VALUES ('GHSA-xxxx-yyyy-zzzz', 'high', 'something upstream', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("recording an advisory: %v", err)
	}
	// One stored route to a live advisory, in the collapsed terms the exposure walk records:
	// the workflow that holds the step, then what it reaches.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		SELECT ?, n.row_id, 'GHSA-xxxx-yyyy-zzzz', 1,
		       json_array(?, 'vendor/tool', 'deep/dependency')
		  FROM abom_node n WHERE n.node_key = 'deep/dependency'`, app, workflow); err != nil {
		t.Fatalf("recording exposure: %v", err)
	}
	return s
}

// findingOn attaches one piece of evidence to a node, which is all the walk reads.
func findingOn(t *testing.T, s *Store, repo int64, nodeKey string) {
	t.Helper()
	res, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, file_path, start_line, start_column,
			message)
		VALUES ('actions/unpinned-tag', ?, ?, 1, 1, 'evidence')`, repo, nodeKey)
	if err != nil {
		t.Fatalf("recording a finding on %s: %v", nodeKey, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading the finding id: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT ?, row_id FROM abom_node WHERE node_key = ?`, id, nodeKey); err != nil {
		t.Fatalf("attaching the finding to %s: %v", nodeKey, err)
	}
}
