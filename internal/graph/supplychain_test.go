package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// supplyChainFixture builds the cases a one-hop test cannot tell apart.
//
//	octo-org/app (seed)  ci.yml      -> actions/checkout
//	                     ci.yml      -> third-party/widget
//	third-party/widget   runs:       -> actions/cache
//	third-party/widget   ci.yml      -> actions/setup-node
//	                     ci.yml      -> its own release.yml -> actions/upload-artifact
//	                     ci.yml      -> its own helper      -> actions/labeler
//
// Only the first three are executed by anything under review. release.yml and helper are
// referenced, but only from a workflow nothing under review reaches.
func supplyChainFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	widget, err := s.InsertUpstreamRepository("third-party/widget")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	w := store.NewWriter(s, 0)
	add := func(n store.Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	external := func(key string) {
		t.Helper()
		add(store.Node{Key: key, Kind: "action", IsExternal: true})
	}

	// Containers before the steps that name them: a writer resolves containment from its cache.
	add(store.Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app})
	add(store.Node{Key: "third-party/widget/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: widget})
	add(store.Node{Key: "third-party/widget/.github/workflows/release.yml", Kind: "workflow", RepositoryID: widget})
	add(store.Node{Key: "third-party/widget/.github/actions/helper", Kind: "composite_action", RepositoryID: widget})
	// The callee node and the container its own steps hang from are one node, exactly as the
	// ingester merges them when the upstream repository is later analyzed.
	external("third-party/widget")
	for _, key := range []string{
		"actions/checkout", "actions/cache", "actions/setup-node",
		"actions/upload-artifact", "actions/labeler",
	} {
		external(key)
	}

	steps := []struct {
		key, container string
		repo           int64
	}{
		{"app-checkout", "octo-org/app/.github/workflows/ci.yml", app},
		{"app-widget", "octo-org/app/.github/workflows/ci.yml", app},
		{"widget-runs", "third-party/widget", widget},
		{"widget-ci-node", "third-party/widget/.github/workflows/ci.yml", widget},
		{"widget-ci-release", "third-party/widget/.github/workflows/ci.yml", widget},
		{"widget-ci-helper", "third-party/widget/.github/workflows/ci.yml", widget},
		{"widget-release", "third-party/widget/.github/workflows/release.yml", widget},
		{"widget-helper", "third-party/widget/.github/actions/helper", widget},
	}
	for _, st := range steps {
		add(store.Node{Key: st.key, Kind: "step", RepositoryID: st.repo, ContainerKey: st.container})
	}

	line := 0
	edge := func(src, dst, kind string, repo int64) {
		t.Helper()
		line++
		if err := w.AddEdge(store.Edge{
			Key: src + "->" + dst, SrcKey: src, DstKey: dst, Kind: kind,
			FilePath: ".github/workflows/ci.yml", StartLine: line, RepositoryID: repo,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", src, err)
		}
	}
	edge("app-checkout", "actions/checkout", "step-uses-action", app)
	edge("app-widget", "third-party/widget", "step-uses-action", app)
	edge("widget-runs", "actions/cache", "composite-step-uses-action", widget)
	edge("widget-ci-node", "actions/setup-node", "step-uses-action", widget)
	edge("widget-ci-release", "third-party/widget/.github/workflows/release.yml",
		"job-uses-reusable-workflow", widget)
	edge("widget-ci-helper", "third-party/widget/.github/actions/helper", "step-uses-action", widget)
	edge("widget-release", "actions/upload-artifact", "step-uses-action", widget)
	edge("widget-helper", "actions/labeler", "composite-step-uses-action", widget)

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

func classify(t *testing.T, s *store.Store) *SupplyChain {
	t.Helper()
	c, err := (&Builder{Store: s}).ClassifyDependencies()
	if err != nil {
		t.Fatalf("ClassifyDependencies: %v", err)
	}
	return c
}

func assertChain(t *testing.T, c *SupplyChain, key string, wantRuntime bool, why string) {
	t.Helper()
	runtime, known := c.Of(key)
	if !known {
		t.Errorf("%s was not classified at all", key)
		return
	}
	if runtime != wantRuntime {
		t.Errorf("%s runtime = %v, want %v: %s", key, runtime, wantRuntime, why)
	}
}

// The base case, and the one a local "is this container referenced by anything" test gets
// backwards: a plain CI workflow in a repository under review is referenced by nothing at all,
// yet the organization runs it every day.
func TestASeedRepositorysOwnWorkflowMakesItsDependenciesRuntime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	assertChain(t, c, "actions/checkout", true,
		"a repository under review names it in its own workflow")
}

// An upstream project's own CI is followed, because a compromised release pipeline is how a
// consumer receives a malicious version, but nothing under review executes it.
func TestAnUpstreamProjectsOwnCIDependencyIsBuildTime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	assertChain(t, c, "actions/setup-node", false,
		"only third-party/widget's own CI runs it")
}

// The whole point of the closure. release.yml is referenced, so a local test calls it runtime —
// but its only caller is a workflow nothing under review reaches, so the claim travels one hop
// further than the evidence does.
func TestABuildTimeOnlyCallerDoesNotPropagateRuntime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	assertChain(t, c, "actions/upload-artifact", false,
		"its caller is reached only from an upstream project's own CI")
}

// Same leak through the other door: being a step inside a composite action's `runs:` block says
// the step runs for whoever calls the action, not that anyone under review does.
func TestACompositeActionNobodyUnderReviewCallsIsBuildTime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	assertChain(t, c, "actions/labeler", false,
		"the composite action holding it is called only from an upstream project's own CI")
}

// The behavior the earlier fix bought, which the exactness work must not undo: once something
// under review calls a composite action, that action's own steps run for it.
func TestACalledCompositeActionsOwnStepsAreRuntime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	assertChain(t, c, "actions/cache", true,
		"octo-org/app calls third-party/widget, whose own steps run inside it")
}

// A key nothing in the graph names is unclassified, which is not the same as build-time. A
// caller that cannot tell them apart would silently demote whatever it has not measured.
func TestANodeTheGraphDoesNotNameIsUnclassifiedRatherThanBuildTime(t *testing.T) {
	c := classify(t, supplyChainFixture(t))

	if _, known := c.Of("actions/nothing-names-this"); known {
		t.Error("an unnamed node was reported as classified")
	}
}
