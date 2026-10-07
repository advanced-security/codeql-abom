package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// agreementFixture takes the shared supply-chain shape and adds the three things the submission
// closure needs in order to walk it: the reference the consumer pinned, the commit that reference
// resolved to, and the run that produced the vendor's own edges.
//
// The exposure walk needs none of that. That asymmetry is the subject of these tests: one walk is
// constrained to the version a repository actually pinned, and the other is not.
func agreementFixture(t *testing.T) *store.Store {
	t.Helper()
	s := supplyChainFixture(t)

	var runID int64
	if err := s.DB().QueryRow(`
		INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
		VALUES ('r-widget', 'abom/edges', 'third-party/widget', 'aaaaaaaaaa',
		        '2026-01-01T00:00:00Z')
		RETURNING row_id`).Scan(&runID); err != nil {
		t.Fatalf("inserting the widget's query run: %v", err)
	}
	// The fixture's edges carry no ref, so the closure has to find the reference spelled the same
	// way: empty. Anything else matches nothing, and every assertion below then passes vacuously.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, analyzed_at)
		VALUES ('third-party/widget', '', 'third-party', 'widget', 1, 'done',
		        '2026-01-01T00:00:00Z', 'aaaaaaaaaa', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding the widget's analyzed ref: %v", err)
	}
	if _, err := s.DB().Exec(
		`UPDATE abom_edge SET query_run_id = ? WHERE edge_key = 'widget-runs->actions/cache'`,
		runID); err != nil {
		t.Fatalf("attributing the widget's shipped edge to its run: %v", err)
	}

	// The shared fixture leaves owner and repo unset on external nodes, because the classifier
	// never reads them. Advisory matching joins on exactly those two columns, so without this the
	// exposure walk has no targets at all and every assertion here passes against a walk that did
	// nothing. Ingestion records them, so setting them is restoring the fixture to real shape.
	if _, err := s.DB().Exec(`
		UPDATE abom_node
		   SET owner = substr(node_key, 1, instr(node_key, '/') - 1),
		       repo  = substr(node_key, instr(node_key, '/') + 1)
		 WHERE is_external = 1 AND COALESCE(owner, '') = '' AND instr(node_key, '/') > 0`); err != nil {
		t.Fatalf("backfilling owner and repo on the external nodes: %v", err)
	}

	// An advisory per action, so each one is a target the exposure walk will actually try. Without
	// them the walk has nothing to do and every "is not exposed" assertion passes for free.
	for _, pkg := range []string{
		"actions/checkout", "actions/cache", "actions/setup-node",
		"actions/upload-artifact", "actions/labeler", "third-party/widget",
	} {
		var adv int64
		if err := s.DB().QueryRow(`
			INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
			VALUES (?, 'high', ?, '2026-01-01T00:00:00Z') RETURNING row_id`,
			"GHSA-"+pkg, "covering "+pkg).Scan(&adv); err != nil {
			t.Fatalf("inserting advisory for %s: %v", pkg, err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_advisory_package (advisory_row_id, ecosystem, package_name,
			                                   vulnerable_version_range)
			VALUES (?, 'actions', ?, '< 99')`, adv, pkg); err != nil {
			t.Fatalf("inserting advisory package %s: %v", pkg, err)
		}
	}
	return s
}

// exposedFrom lists what the exposure walk says a repository reaches, keyed by node.
func exposedFrom(t *testing.T, s *store.Store, repository string) map[string]int {
	t.Helper()
	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}
	rows, err := s.DB().Query(`
		SELECT n.node_key, x.hops
		  FROM abom_exposure x
		  JOIN abom_node n  ON n.row_id = x.node_row_id
		  JOIN repository r ON r.row_id = x.repository_row_id
		 WHERE r.repository_full_name = ?`, repository)
	if err != nil {
		t.Fatalf("reading exposure: %v", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var key string
		var hops int
		if err := rows.Scan(&key, &hops); err != nil {
			t.Fatalf("scanning exposure: %v", err)
		}
		out[key] = hops
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating exposure: %v", err)
	}
	return out
}

// submittedFrom lists what the submission closure says a repository depends on.
func submittedFrom(t *testing.T, s *store.Store, repository string) map[string]bool {
	t.Helper()
	entries, err := s.SubmissionClosure(repository)
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		key := e.Owner + "/" + e.Repo
		if e.Subpath != "" {
			key += "/" + e.Subpath
		}
		out[key] = true
	}
	return out
}

// A dependency's own CI is not part of a consumer's supply chain.
func TestNeitherWalkReachesThroughADependencysOwnCI(t *testing.T) {
	s := agreementFixture(t)

	exposed := exposedFrom(t, s, "octo-org/app")
	submitted := submittedFrom(t, s, "octo-org/app")
	if len(exposed) == 0 {
		t.Fatal("the exposure walk found nothing, so the exclusions below would be vacuous")
	}

	// These sit behind containers only the widget's CI reaches.
	for _, ci := range []string{"actions/setup-node", "actions/upload-artifact", "actions/labeler"} {
		if hops, ok := exposed[ci]; ok {
			t.Errorf("exposure reached %s at %d hops; it belongs to the widget's own CI and not "+
				"to octo-org/app", ci, hops)
		}
		if submitted[ci] {
			t.Errorf("the submission closure carried %s, which the widget only uses to test "+
				"itself", ci)
		}
	}
}

// The shipped dependency has to be reported by both, or the test above would pass against a walk
// that reaches nothing at all.
func TestBothWalksReachWhatTheDependencyShips(t *testing.T) {
	s := agreementFixture(t)

	exposed := exposedFrom(t, s, "octo-org/app")
	if hops, ok := exposed["actions/cache"]; !ok {
		t.Errorf("exposure missed actions/cache, which the widget runs; it reported %v", exposed)
	} else if hops != 2 {
		t.Errorf("actions/cache is %d hops from octo-org/app, want 2 via third-party/widget", hops)
	}
	if !submittedFrom(t, s, "octo-org/app")["actions/cache"] {
		t.Error("the submission closure missed actions/cache, which the widget runs")
	}
}

// The two walks must agree on the whole supply chain, not only the fixture's named actions.
func TestEveryExposureNamesSomethingTheSubmissionWouldAlsoCarry(t *testing.T) {
	s := agreementFixture(t)

	submitted := submittedFrom(t, s, "octo-org/app")
	if len(submitted) == 0 {
		t.Fatal("the submission closure is empty, so agreement below would be vacuous")
	}
	exposed := exposedFrom(t, s, "octo-org/app")
	if len(exposed) == 0 {
		t.Fatal("the exposure walk found nothing, so agreement below would be vacuous")
	}
	parts, err := ClosureAgreement(s, []string{"octo-org/app"})
	if err != nil {
		t.Fatalf("ClosureAgreement: %v", err)
	}
	if got := partNamed(t, parts, closureExposurePart); got.Compared != len(exposed) {
		t.Errorf("check 8 compared %d exposures of octo-org/app, want all %d", got.Compared,
			len(exposed))
	}
	for _, d := range partNamed(t, parts, closureExposurePart).Disagreements {
		t.Errorf("%s is reported exposed and the submission closure does not carry it: the two "+
			"walks disagree about one supply chain (%s)", d.Subject, d.Independent)
	}
}

// pinnedVersionFixture builds one dependency analyzed at two refs while the consumer pins v1:
//
//	consumer/app   ci.yml     -> vendor/action@v1
//	vendor/action  action.yml -> helper/tool   produced by the run for v1, so it is the consumer's
//	vendor/action  action.yml -> newer/tool    produced by the run for v2, so it is not
//
// The shared supply-chain fixture has no per-ref vendor edges.
func pinnedVersionFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	consumer, err := s.UpsertRepository("consumer/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	vendor, err := s.InsertUpstreamRepository("vendor/action")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	run := func(runKey, sha string) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(`
			INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
			VALUES (?, 'abom/edges', 'vendor/action', ?, '2026-01-01T00:00:00Z')
			RETURNING row_id`, runKey, sha).Scan(&id); err != nil {
			t.Fatalf("inserting query run %s: %v", runKey, err)
		}
		return id
	}
	atV1, atV2 := run("r-v1", "aaaaaaaaaa"), run("r-v2", "bbbbbbbbbb")

	w := store.NewWriter(s, 0)
	add := func(n store.Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	// Containers before the steps that name them, and owner and repo on every external node
	// because advisory matching joins on exactly those two columns.
	add(store.Node{Key: "consumer/app/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: consumer})
	add(store.Node{Key: "vendor/action", Kind: "action", IsExternal: true,
		Owner: "vendor", Repo: "action"})
	add(store.Node{Key: "helper/tool", Kind: "action", IsExternal: true,
		Owner: "helper", Repo: "tool"})
	add(store.Node{Key: "newer/tool", Kind: "action", IsExternal: true,
		Owner: "newer", Repo: "tool"})
	add(store.Node{Key: "app-step", Kind: "step", RepositoryID: consumer,
		ContainerKey: "consumer/app/.github/workflows/ci.yml"})
	// The step inside the vendor's manifest hangs off the vendor's own node, which is what lets a
	// walk cross from what a consumer references into what that dependency runs.
	add(store.Node{Key: "vendor-manifest-step", Kind: "step", RepositoryID: vendor,
		ContainerKey: "vendor/action"})

	edge := func(e store.Edge) {
		t.Helper()
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	edge(store.Edge{Key: "app->vendor", SrcKey: "app-step", DstKey: "vendor/action",
		Kind: "step-uses-action", RefValue: "v1", FilePath: ".github/workflows/ci.yml",
		StartLine: 1, RepositoryID: consumer})
	edge(store.Edge{Key: "vendor->helper", SrcKey: "vendor-manifest-step", DstKey: "helper/tool",
		Kind: "composite-step-uses-action", FilePath: "action.yml", StartLine: 2,
		RepositoryID: vendor, QueryRunID: atV1})
	edge(store.Edge{Key: "vendor->newer", SrcKey: "vendor-manifest-step", DstKey: "newer/tool",
		Kind: "composite-step-uses-action", FilePath: "action.yml", StartLine: 3,
		RepositoryID: vendor, QueryRunID: atV2})
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, analyzed_at)
		VALUES ('vendor/action','v1','vendor','action',1,'done','2026-01-01T00:00:00Z',
		        'aaaaaaaaaa','2026-01-01T00:00:00Z'),
		       ('vendor/action','v2','vendor','action',1,'done','2026-01-01T00:00:00Z',
		        'bbbbbbbbbb','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("seeding the vendor's analyzed refs: %v", err)
	}
	for _, pkg := range []string{"helper/tool", "newer/tool"} {
		var adv int64
		if err := s.DB().QueryRow(`
			INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
			VALUES (?, 'high', ?, '2026-01-01T00:00:00Z') RETURNING row_id`,
			"GHSA-"+pkg, "covering "+pkg).Scan(&adv); err != nil {
			t.Fatalf("inserting advisory for %s: %v", pkg, err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_advisory_package (advisory_row_id, ecosystem, package_name,
			                                   vulnerable_version_range)
			VALUES (?, 'actions', ?, '< 99')`, adv, pkg); err != nil {
			t.Fatalf("inserting advisory package %s: %v", pkg, err)
		}
	}
	return s
}

// A child belongs to the pinned version of its parent in both walks.
// Exposure must carry the commit a route was read from, as the submission closure does.
func TestBothWalksHonorTheVersionTheRepositoryPinned(t *testing.T) {
	s := pinnedVersionFixture(t)

	submitted := submittedFrom(t, s, "consumer/app")
	if !submitted["helper/tool"] {
		t.Fatal("the submission closure missed the child of the pinned version, so the " +
			"exclusion below would be vacuous")
	}
	if submitted["newer/tool"] {
		t.Error("the submission closure carried a child of a version consumer/app does not pin")
	}

	exposed := exposedFrom(t, s, "consumer/app")
	if _, ok := exposed["helper/tool"]; !ok {
		t.Errorf("exposure missed the child of the pinned version; it reported %v", exposed)
	}
	if hops, ok := exposed["newer/tool"]; ok {
		t.Errorf("consumer/app pins vendor/action@v1 and is reported exposed to newer/tool at "+
			"%d hops; newer/tool only enters the vendor at v2, so the exposure walk and the "+
			"submission closure disagree about one supply chain", hops)
	}
}
