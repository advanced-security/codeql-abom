package graphexport

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A fixture with the awkward cases in it: a node nothing has measured, two relationships between
// one pair, and a grouping to cluster by.
func fixture() store.Graph {
	return store.Graph{
		Nodes: []store.GraphNode{
			{Key: "octo/widget", Kind: "action", Owner: "octo", Repository: "octo/widget",
				DependentRepos: 4, ReachKnown: true, Findings: 2, Rules: []string{"actions/unpinned-tag"}},
			{Key: "acme/build", Kind: "action", Owner: "acme", Repository: "acme/build",
				ReachKnown: false, IsExternal: true},
			// A repository's own files carry no Owner: the projection sets that for dependencies.
			{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow",
				Repository: "octo/app", DependentRepos: 0, ReachKnown: true},
		},
		Edges: []store.GraphEdge{
			{Source: "octo/app/.github/workflows/ci.yml", Target: "octo/widget",
				Kind: "step-uses-action", Boundary: "same-owner", Refs: []string{"v4"}, Count: 1},
			{Source: "octo/app/.github/workflows/ci.yml", Target: "octo/widget",
				Kind: "step-uses-action", Boundary: "same-owner", Refs: []string{"v5"}, Count: 1},
		},
	}
}

// A drawing that cannot be reproduced cannot be regression-tested, and the encoder is the first
// place that can go wrong: a map iterated in Go's order emits a different file every run.
func TestTheSameGraphEncodesByteForByte(t *testing.T) {
	first, err := DOT(fixture(), Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	second, err := DOT(fixture(), Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two encodings of one graph differ")
	}

	// The store is free to return rows in any order; the file must not move because it did.
	shuffled := fixture()
	shuffled.Nodes[0], shuffled.Nodes[2] = shuffled.Nodes[2], shuffled.Nodes[0]
	shuffled.Edges[0], shuffled.Edges[1] = shuffled.Edges[1], shuffled.Edges[0]
	third, err := DOT(shuffled, Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if string(first) != string(third) {
		t.Error("the encoding depends on the order rows arrived in")
	}
}

// Node keys carry paths and job identifiers out of files ABOM did not write. Inside a quoted DOT
// string only `"` is escaped, so an unescaped one ends the identifier early and everything after
// it is read as syntax.
func TestAQuoteInAKeyDoesNotEndTheIdentifierEarly(t *testing.T) {
	g := store.Graph{Nodes: []store.GraphNode{
		{Key: `odd/re"po`, Kind: "action", ReachKnown: true},
		{Key: `trailing\`, Kind: "action", ReachKnown: true},
	}}
	out, err := DOT(g, Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if !strings.Contains(string(out), `"odd/re\"po"`) {
		t.Errorf("the quote was not escaped:\n%s", out)
	}
	// A backslash last would escape the closing quote and swallow the rest of the file.
	if strings.Contains(string(out), `"trailing\"`+"\n") {
		t.Errorf("a trailing backslash escaped the closing quote:\n%s", out)
	}
}

// Two `uses:` lines naming one action are two relationships. `strict` would silently fold them
// into one, which is the same identity loss that once collapsed every edge onto a single row.
func TestTwoReferencesToOneTargetStayTwoEdges(t *testing.T) {
	out, err := DOT(fixture(), Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(out)), "strict") {
		t.Error("the graph is strict, which collapses multi-edges")
	}
	if n := strings.Count(string(out), "->"); n != 2 {
		t.Errorf("%d edges written, want both references kept:\n%s", n, out)
	}
}

// Reach is recomputed wholesale, so a node ingested since the last pass has no measurement. DOT
// has no null, which makes this the one place the format actively invites the wrong answer.
func TestANodeWithNoReachIsNotWrittenAsZero(t *testing.T) {
	out, err := DOT(fixture(), Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	line := statementFor(t, string(out), `"acme/build"`)
	if strings.Contains(line, "abom_dependent_repos") {
		t.Errorf("an unmeasured node carries a reach count: %s", line)
	}
	if !strings.Contains(line, `abom_reach_known="false"`) {
		t.Errorf("an unmeasured node does not say so: %s", line)
	}

	// A measured zero is a different fact and has to stay tellable apart from the above.
	measured := statementFor(t, string(out), `"octo/app/.github/workflows/ci.yml"`)
	if !strings.Contains(measured, `abom_dependent_repos="0"`) {
		t.Errorf("a measured zero lost its count: %s", measured)
	}
}

// A scoped export that does not say what it left out is the coverage lie a filter turns into.
func TestTheExportSaysWhatItIsAViewOf(t *testing.T) {
	out, err := DOT(fixture(), Options{Predicate: "scope=owned&flags=findings"})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	for _, want := range []string{
		`abom_predicate="scope=owned&flags=findings"`,
		`abom_nodes="3"`,
		`abom_edges="2"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the document does not state %s:\n%s", want, out)
		}
	}
}

// A line beginning with `#` is discarded as a preprocessor marker, so a node key starting with one
// would vanish from the file without any error being reported.
func TestNoStatementBeginsWithAHash(t *testing.T) {
	g := fixture()
	g.Nodes = append(g.Nodes, store.GraphNode{Key: "#hash/first", Kind: "action", ReachKnown: true})
	out, err := DOT(g, Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	for i, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "#") {
			t.Errorf("line %d begins with a preprocessor marker: %q", i+1, line)
		}
	}
}

// Graphviz draws a cluster as a box around its members, which is what replaces the hand-rolled
// hulls. A graph and its subgraphs share one namespace, so two groups with one name is one box.
func TestGroupingEmitsOneUniquelyNamedClusterPerGroup(t *testing.T) {
	out, err := DOT(fixture(), Options{GroupBy: "owner"})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "subgraph ") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, "subgraph "), " {")
		if names[name] {
			t.Errorf("two subgraphs are called %s, so they are one box", name)
		}
		names[name] = true
		if !strings.Contains(name, "cluster") {
			t.Errorf("%s is not a cluster, so nothing draws a boundary for it", name)
		}
	}
	if len(names) != 2 {
		t.Errorf("%d clusters for 2 owners:\n%s", len(names), out)
	}
}

// Ungrouped, there is no claim that a set is complete, so nothing is boxed.
func TestWithoutAGroupingNothingIsClustered(t *testing.T) {
	out, err := DOT(fixture(), Options{})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if strings.Contains(string(out), "subgraph") {
		t.Errorf("an ungrouped export drew a boundary:\n%s", out)
	}
}

// statementFor returns the line declaring id, so an assertion about one node cannot pass because
// some other line in the file happened to carry the attribute.
func statementFor(t *testing.T, out, id string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, id+" [") {
			return trimmed
		}
	}
	t.Fatalf("no statement for %s in:\n%s", id, out)
	return ""
}

// Owner names the account a *dependency* lives under, and a repository's own workflows and jobs
// carry the repository instead. Grouping that read Owner alone boxed the dependencies and left
// everything the organization actually owns loose on the canvas -- which is the opposite of the
// reading the grouping exists to give.
func TestOwnerGroupingReadsTheRepositoryWhenNoOwnerIsSet(t *testing.T) {
	out, err := DOT(fixture(), Options{GroupBy: "owner"})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	cluster := clusterHolding(string(out), `"octo/app/.github/workflows/ci.yml"`)
	if cluster != `"cluster_owner_octo"` {
		t.Errorf("the workflow is in %s, want its repository's account", cluster)
	}
}

// An account node *is* the group. Drawing it inside its own box puts the container inside the
// container, and makes the box's membership a claim about itself.
func TestAnAccountNodeIsNotAMemberOfItsOwnCluster(t *testing.T) {
	g := store.Graph{Nodes: []store.GraphNode{
		{Key: "org:octo", Kind: store.OrgNodeKind, Owner: "octo", ReachKnown: true},
		{Key: "octo/widget", Kind: "action", Owner: "octo", ReachKnown: true},
	}}
	out, err := DOT(g, Options{GroupBy: "owner"})
	if err != nil {
		t.Fatalf("DOT: %v", err)
	}
	if cluster := clusterHolding(string(out), `"org:octo"`); cluster != "" {
		t.Errorf("the account node is inside %s", cluster)
	}
	if cluster := clusterHolding(string(out), `"octo/widget"`); cluster == "" {
		t.Errorf("the member was not clustered:\n%s", out)
	}
}

// clusterHolding names the subgraph a statement sits inside, or "" when it is at the top level.
func clusterHolding(out, id string) string {
	current := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "subgraph "):
			current = strings.TrimSuffix(strings.TrimPrefix(trimmed, "subgraph "), " {")
		case trimmed == "}":
			current = ""
		case strings.HasPrefix(trimmed, id+" ["):
			return current
		}
	}
	return ""
}
