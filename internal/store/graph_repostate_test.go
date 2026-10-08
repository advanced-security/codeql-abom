package store

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/normalize"
)

// A repository has three states: analyzed, queued, and holding nothing to analyze.
// Repository nodes are keyed `repo:owner/name`; frontier rows are keyed `owner/name`.
func TestARepositoryCarriesTheAnalysisStateOfItsOwnQueueRow(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/service")
	writeNode(t, s, Node{Key: normalize.RepoKey("acme/service"), Kind: "repo", RepositoryID: id})
	enqueueDone(t, s, "acme/service")

	node := repoNode(t, s, "acme/service")

	if !node.Analyzed {
		t.Error("a repository analyzed minutes ago reports that it was not")
	}
	if node.Discovery != "done" {
		t.Errorf("Discovery = %q, want \"done\"", node.Discovery)
	}
}

// "We have not looked yet" and "there was nothing to look at" are different coverage statements,
// and only one of them is work somebody can do.
func TestARepositoryWithNothingToAnalyzeIsNotDrawnAsUnexamined(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/empty")
	writeNode(t, s, Node{Key: normalize.RepoKey("acme/empty"), Kind: "repo", RepositoryID: id})
	enqueueWithStatus(t, s, "acme/empty", "unavailable")

	node := repoNode(t, s, "acme/empty")

	if node.Discovery != "unavailable" {
		t.Errorf("Discovery = %q, want \"unavailable\"", node.Discovery)
	}
	if node.Analyzed {
		t.Error("a repository holding nothing to analyze reports that it was analyzed")
	}
}

// A repository queued and not yet claimed must say so. Reporting nothing leaves it identical to a
// repository that has never been queued at all, which is not coverage.
func TestAQueuedRepositorySaysItIsQueued(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/waiting")
	writeNode(t, s, Node{Key: normalize.RepoKey("acme/waiting"), Kind: "repo", RepositoryID: id})
	enqueueWithStatus(t, s, "acme/waiting", "pending")

	node := repoNode(t, s, "acme/waiting")

	if node.Discovery != "queued" {
		t.Errorf("Discovery = %q, want \"queued\"", node.Discovery)
	}
	if node.Analyzed {
		t.Error("a repository still waiting in the queue reports that it was analyzed")
	}
}

// Read in full and found empty is settled. A branch that could not be fetched is not, and the
// least settled reference is the one a repository reports, whichever query draws it.
func TestABranchThatCouldNotBeFetchedOutranksNothingToAnalyze(t *testing.T) {
	for _, drawn := range []string{"attached", "drawn"} {
		t.Run(drawn, func(t *testing.T) {
			s := newTestStore(t)
			id := seedOwnedRepo(t, s, "acme/docs")
			if drawn == "drawn" {
				writeNode(t, s, Node{Key: normalize.RepoKey("acme/docs"), Kind: "repo", RepositoryID: id})
			}
			enqueueWithStatus(t, s, "acme/docs", "nothing")

			if node := repoNode(t, s, "acme/docs"); node.Discovery != "nothing" || node.Analyzed {
				t.Errorf("Discovery = %q, analyzed = %v, want nothing to analyze and not analyzed",
					node.Discovery, node.Analyzed)
			}

			enqueueRef(t, s, "acme/docs", "release", 0, "unavailable")
			if got := repoNode(t, s, "acme/docs").Discovery; got != "unavailable" {
				t.Errorf("Discovery = %q, want unavailable", got)
			}
			if drawn != "drawn" {
				return
			}
			generation, err := s.RecordGraphGeneration(GraphRebuild{})
			if err != nil {
				t.Fatalf("RecordGraphGeneration: %v", err)
			}
			delta, err := s.GraphDelta(generation, FullGraphOptions{})
			if err != nil {
				t.Fatalf("GraphDelta: %v", err)
			}
			for _, m := range delta.Metrics {
				if m.Key == normalize.RepoKey("acme/docs") && m.Discovery != "unavailable" {
					t.Errorf("delta Discovery = %q, want unavailable", m.Discovery)
				}
			}
		})
	}
}

// The prefixed key must not start matching dependencies. `repo:actions/checkout` and the queue row
// for `actions/checkout` are the same project, and conflating a repository node with the
// dependency row would report an action's analysis as its repository's.
func TestADependencyQueueRowIsNotReadAsItsRepositoryNode(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/service")
	// An action node, not a repository node, sharing the queue row's key exactly.
	writeNode(t, s, Node{
		Key: "actions/checkout", Kind: "action", RepositoryID: id,
		Owner: "actions", Repo: "checkout", IsExternal: true})
	enqueueDone(t, s, "actions/checkout")

	nodes := allNodes(t, s)
	action, ok := nodes["actions/checkout"]
	if !ok {
		t.Fatal("the action node is missing")
	}

	if !action.Analyzed {
		t.Error("an unprefixed node stopped matching its own queue row")
	}
}

// An owned repository must be drawn even before anything has analyzed it.
func TestAnOwnedRepositoryIsDrawnBeforeAnythingHasAnalyzedIt(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/waiting")
	enqueueWithStatus(t, s, "acme/waiting", "pending")

	node := repoNode(t, s, "acme/waiting")

	if node.Kind != "repo" {
		t.Errorf("Kind = %q, want \"repo\"", node.Kind)
	}
	if node.Discovery != "queued" {
		t.Errorf("Discovery = %q, want \"queued\"", node.Discovery)
	}
}

// Drawing it is not enough: a node whose reach nobody measured must not report a measurement.
// Sizing means reach, so `reachKnown` true with zero dependents asserts that nothing depends on a
// repository nobody has looked inside.
func TestARepositoryNobodyAnalyzedDoesNotClaimItsReachIsKnown(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/waiting")
	enqueueWithStatus(t, s, "acme/waiting", "pending")

	node := repoNode(t, s, "acme/waiting")

	if node.ReachKnown {
		t.Error("an unanalyzed repository claims its reach is known")
	}
}

// A repository outside the declared scope is not drawn just because the corpus has heard of it.
// Every upstream project's own repository would otherwise arrive as a node nobody asked for.
func TestAnUndeclaredRepositoryIsNotDrawnFromNothing(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")
	if _, err := s.InsertUpstreamRepository("stranger/thing"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	if _, ok := allNodes(t, s)[normalize.RepoKey("stranger/thing")]; ok {
		t.Error("an undeclared repository was drawn")
	}
}

// The synthesized node must not duplicate one that ingestion already wrote, or a repository
// appears twice and every count over the graph doubles it.
func TestAnAnalyzedRepositoryIsNotDrawnTwice(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/service")
	writeNode(t, s, Node{Key: normalize.RepoKey("acme/service"), Kind: "repo", RepositoryID: id})
	enqueueDone(t, s, "acme/service")

	var seen int
	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Kind == "repo" && n.Repository == "acme/service" {
			seen++
		}
	}

	if seen != 1 {
		t.Errorf("drew %d nodes for one repository, want 1", seen)
	}
}

// A project the operator owns is two nodes: the repository, and the same project as a dependency
// others use at particular references. They answer different questions, and the repository must
// answer only its own. `advanced-security/filter-sarif` is analyzed at its default branch and
// queued at `main` and `v1.1` because somebody else uses those, and reporting the repository as
// queued says the operator's own work is unanalyzed when it is not.
func TestARepositoryDoesNotInheritTheQueueStateOfItselfAsADependency(t *testing.T) {
	s := newTestStore(t)
	id := seedOwnedRepo(t, s, "acme/action")
	writeNode(t, s, Node{Key: normalize.RepoKey("acme/action"), Kind: "repo", RepositoryID: id})
	enqueueDone(t, s, "acme/action")
	enqueueRef(t, s, "acme/action", "v1", 1, "pending")

	node := repoNode(t, s, "acme/action")

	if node.Discovery != "done" {
		t.Errorf("Discovery = %q, want \"done\" — the repository itself was analyzed", node.Discovery)
	}
	if !node.Analyzed {
		t.Error("the repository reports unanalyzed because a dependent uses it at a queued ref")
	}
}

// The dependency node keeps reporting every reference, which is the coverage question it exists to
// answer. Scoping the repository to its root row must not silence it.
func TestTheDependencyNodeStillReportsItsQueuedReferences(t *testing.T) {
	s := newTestStore(t)
	enqueueDone(t, s, "acme/action")
	enqueueRef(t, s, "acme/action", "v1", 1, "pending")
	nodes := []GraphNode{{Key: "acme/action", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "caller/repo", Target: "acme/action", Refs: []string{"v1"}}}

	out, _, err := s.splitNodesByRef(nodes, edges)
	if err != nil {
		t.Fatalf("splitNodesByRef: %v", err)
	}

	var found bool
	for _, n := range out {
		if n.Key != "acme/action@v1" {
			continue
		}
		found = true
		if n.Discovery != "queued" {
			t.Errorf("Discovery = %q, want \"queued\"", n.Discovery)
		}
	}
	if !found {
		t.Errorf("no per-reference node for the queued ref; got %d nodes", len(out))
	}
}

func seedOwnedRepo(t *testing.T, s *Store, fullName string) int64 {
	t.Helper()
	owner, _, _ := splitRepository(fullName)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: owner, Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	id, err := s.InsertOwnedRepository(fullName)
	if err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}
	return id
}

func writeNode(t *testing.T, s *Store, n Node) {
	t.Helper()
	w := NewWriter(s, 0)
	if err := w.AddNode(n); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func enqueueDone(t *testing.T, s *Store, nodeKey string) {
	t.Helper()
	enqueueWithStatus(t, s, nodeKey, "done")
}

func enqueueWithStatus(t *testing.T, s *Store, nodeKey, status string) {
	t.Helper()
	owner, repo, _ := splitRepository(nodeKey)
	if _, err := s.db.Exec(
		`INSERT INTO abom_frontier (node_key, owner, repo, depth, status, enqueued_at)
		 VALUES (?, ?, ?, 0, ?, datetime('now'))`,
		nodeKey, owner, repo, status); err != nil {
		t.Fatalf("enqueueing %q: %v", nodeKey, err)
	}
}

// enqueueRef adds the row that exists because somebody else uses this project at a reference.
func enqueueRef(t *testing.T, s *Store, nodeKey, ref string, depth int, status string) {
	t.Helper()
	owner, repo, _ := splitRepository(nodeKey)
	if _, err := s.db.Exec(
		`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'))`,
		nodeKey, ref, owner, repo, depth, status); err != nil {
		t.Fatalf("enqueueing %q@%s: %v", nodeKey, ref, err)
	}
}

// enqueueAnalyzed adds a reference row that has been analyzed, at a stated time.
func enqueueAnalyzed(t *testing.T, s *Store, nodeKey, ref, analyzedAt string) {
	t.Helper()
	owner, repo, _ := splitRepository(nodeKey)
	if _, err := s.db.Exec(
		`INSERT INTO abom_frontier
		     (node_key, ref, owner, repo, depth, status, enqueued_at, analyzed_at)
		 VALUES (?, ?, ?, ?, 1, 'done', datetime('now'), ?)`,
		nodeKey, ref, owner, repo, analyzedAt); err != nil {
		t.Fatalf("enqueueing %q@%s: %v", nodeKey, ref, err)
	}
}

func allNodes(t *testing.T, s *Store) map[string]GraphNode {
	t.Helper()
	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	out := map[string]GraphNode{}
	for _, n := range g.Nodes {
		out[n.Key] = n
	}
	return out
}

func repoNode(t *testing.T, s *Store, fullName string) GraphNode {
	t.Helper()
	nodes := allNodes(t, s)
	n, ok := nodes[normalize.RepoKey(fullName)]
	if !ok {
		t.Fatalf("no repository node for %q; graph holds %d nodes", fullName, len(nodes))
	}
	return n
}

// A push held for code scanning's own analysis is queued and deliberately not claimed yet, which
// is neither work about to run nor a retry, whichever query draws the repository.
func TestARepositoryHeldForCodeScanningSaysItIsWaiting(t *testing.T) {
	for _, drawn := range []string{"attached", "drawn"} {
		t.Run(drawn, func(t *testing.T) {
			s := newTestStore(t)
			id := seedOwnedRepo(t, s, "acme/service")
			if drawn == "drawn" {
				writeNode(t, s, Node{Key: normalize.RepoKey("acme/service"), Kind: "repo", RepositoryID: id})
			}
			enqueueWithStatus(t, s, "acme/service", "pending")
			if _, err := s.db.Exec(`UPDATE abom_frontier SET attempts = 1, awaiting_code_scanning = 'c2'
				WHERE node_key = 'acme/service'`); err != nil {
				t.Fatal(err)
			}

			if node := repoNode(t, s, "acme/service"); node.Discovery != "waiting" || node.Analyzed {
				t.Errorf("Discovery = %q, analyzed = %v, want waiting and not analyzed", node.Discovery, node.Analyzed)
			}
		})
	}
}
