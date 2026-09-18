// Package graphexport renders the graph projection as documents other tools read.
//
// It mirrors internal/bomexport: the store owns the facts, this owns how they are written down.
// What it encodes is store.Graph, already scoped and filtered, so an export answers exactly the
// question the interface was showing rather than a second one assembled here.
package graphexport

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Options selects what the exported document says about itself.
type Options struct {
	// Name identifies the graph. Empty is "abom".
	Name string
	// Predicate is the filter text this projection came from, carried verbatim. Empty means
	// nothing narrowed it; a file omitting this would look like the whole corpus whatever it
	// actually held.
	Predicate string
	// GroupBy draws one cluster per distinct value: "owner", "repository" or "workflow". Empty
	// draws none, because ungrouped the file claims no set is complete.
	GroupBy string
}

// DOT renders the projection as a Graphviz digraph.
//
// Every statement is indented, which is also what keeps one safe: the DOT parser discards a line
// beginning with `#` as a preprocessor marker, so a node key starting with one would vanish from
// an unindented file with no error reported anywhere.
func DOT(g store.Graph, opts Options) ([]byte, error) {
	name := opts.Name
	if name == "" {
		name = "abom"
	}

	var b strings.Builder
	// Never `strict`. It folds relationships sharing a pair of endpoints into one, which is the
	// identity loss this export exists to avoid: two `uses:` lines naming one action are two
	// relationships, told apart by the references carried on the edges.
	fmt.Fprintf(&b, "digraph %s {\n", quote(name))
	writeStatement(&b, 1, "graph", documentAttrs(g, opts))
	b.WriteByte('\n')

	nodes := append([]store.GraphNode(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })

	if opts.GroupBy == "" {
		for _, n := range nodes {
			writeStatement(&b, 1, quote(n.Key), nodeAttrs(n))
		}
	} else {
		writeClusters(&b, nodes, opts.GroupBy)
	}

	if len(g.Edges) > 0 {
		b.WriteByte('\n')
	}
	for _, e := range sortedEdges(g.Edges) {
		writeStatement(&b, 1, quote(e.Source)+" -> "+quote(e.Target), edgeAttrs(e))
	}

	b.WriteString("}\n")
	return []byte(b.String()), nil
}

// writeClusters boxes each group and leaves the rest at the top level.
//
// A node with no value for the grouping belongs to no group, and is never put in one named for
// the absence: a box asserts its members belong together, and "these have nothing in common" is
// not that.
func writeClusters(b *strings.Builder, nodes []store.GraphNode, by string) {
	members := map[string][]store.GraphNode{}
	var ungrouped []store.GraphNode
	for _, n := range nodes {
		if key := groupKey(n, by); key != "" {
			members[key] = append(members[key], n)
			continue
		}
		ungrouped = append(ungrouped, n)
	}

	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		// A graph and its subgraphs share one namespace, so two clusters with one name are one
		// box. The group's own value is what keeps each distinct.
		fmt.Fprintf(b, "  subgraph %s {\n", quote("cluster_"+by+"_"+key))
		writeStatement(b, 2, "graph", [][2]string{{"label", key}, {"abom_group", by}})
		for _, n := range members[key] {
			writeStatement(b, 2, quote(n.Key), nodeAttrs(n))
		}
		b.WriteString("  }\n")
	}
	for _, n := range ungrouped {
		writeStatement(b, 1, quote(n.Key), nodeAttrs(n))
	}
}

// groupKey is the value a node is clustered by, or "" for one that belongs to no group.
//
// Owner follows store.ownerOf rather than reading GraphNode.Owner, which names the account a
// *dependency* lives under: a repository's own workflows and jobs carry the repository instead,
// so reading the field alone boxed the dependencies and left everything the organization owns
// loose. A node with neither -- a container image, say -- is grouped by nothing rather than by a
// name invented for the absence.
func groupKey(n store.GraphNode, by string) string {
	switch by {
	case "owner":
		// The account node is the group, not a member of it.
		if n.Kind == store.OrgNodeKind {
			return ""
		}
		if owner, _, ok := strings.Cut(n.Repository, "/"); ok {
			return owner
		}
		return n.Owner
	case "repository":
		return n.Repository
	case "workflow":
		return n.Container
	default:
		return ""
	}
}

// documentAttrs states what the file is a view of.
//
// `abom_terms_not_applied` is the sharpest of them: those terms narrow nothing here, because the
// payload expects whoever draws it to apply them and Graphviz will not. A file that stayed quiet
// about that would read as the answer to a narrower question than it is.
func documentAttrs(g store.Graph, opts Options) [][2]string {
	attrs := [][2]string{
		{"abom_predicate", opts.Predicate},
		{"abom_nodes", strconv.Itoa(len(g.Nodes))},
		{"abom_edges", strconv.Itoa(len(g.Edges))},
		{"abom_includes_steps", strconv.FormatBool(g.IncludesSteps)},
	}
	if len(g.ClientApplied) > 0 {
		applied := append([]string(nil), g.ClientApplied...)
		sort.Strings(applied)
		attrs = append(attrs, [2]string{"abom_terms_not_applied", strings.Join(applied, ",")})
	}
	if opts.GroupBy != "" {
		attrs = append(attrs, [2]string{"abom_group_by", opts.GroupBy})
	}
	return attrs
}

// nodeAttrs writes what the interface filters on, and refuses to write what nobody measured.
func nodeAttrs(n store.GraphNode) [][2]string {
	attrs := [][2]string{{"abom_kind", n.Kind}}
	attrs = appendIf(attrs, "abom_repository", n.Repository)
	attrs = appendIf(attrs, "abom_owner", n.Owner)
	attrs = appendIf(attrs, "abom_origin", n.Origin)
	attrs = appendIf(attrs, "abom_grouping", n.Grouping)
	attrs = appendIf(attrs, "abom_ref", n.Ref)
	attrs = appendIf(attrs, "abom_discovery", n.Discovery)
	attrs = append(attrs,
		[2]string{"abom_external", strconv.FormatBool(n.IsExternal)},
		[2]string{"abom_analyzed", strconv.FormatBool(n.Analyzed)},
		[2]string{"abom_reach_known", strconv.FormatBool(n.ReachKnown)},
	)
	// DOT has no null. Writing zero for a node reach was never computed for asserts a measurement
	// nobody made, in the one attribute that decides how large it is drawn.
	if n.ReachKnown {
		attrs = append(attrs, [2]string{"abom_dependent_repos", strconv.Itoa(n.DependentRepos)})
	}
	attrs = append(attrs,
		[2]string{"abom_findings", strconv.Itoa(n.Findings)},
		[2]string{"abom_advisories", strconv.Itoa(n.Advisories)},
		[2]string{"abom_refs_in_use", strconv.Itoa(n.RefsInUse)},
		[2]string{"abom_refs_analyzed", strconv.Itoa(n.RefsAnalyzed)},
	)
	if len(n.Rules) > 0 {
		rules := append([]string(nil), n.Rules...)
		sort.Strings(rules)
		attrs = append(attrs, [2]string{"abom_rules", strings.Join(rules, ",")})
	}
	return attrs
}

// edgeAttrs writes the relationship's own facts, including the references that distinguish it
// from another relationship between the same pair.
func edgeAttrs(e store.GraphEdge) [][2]string {
	attrs := [][2]string{{"abom_kind", e.Kind}}
	// An edge whose ends nobody could name has no boundary, which is unknown rather than a
	// fourth class. Left absent it would read as one of the three.
	if e.Boundary == "" {
		attrs = append(attrs, [2]string{"abom_boundary_known", "false"})
	} else {
		attrs = append(attrs, [2]string{"abom_boundary", e.Boundary})
	}
	if len(e.Refs) > 0 {
		refs := append([]string(nil), e.Refs...)
		sort.Strings(refs)
		attrs = append(attrs, [2]string{"abom_refs", strings.Join(refs, ",")})
	}
	return append(attrs, [2]string{"abom_count", strconv.Itoa(e.Count)})
}

// sortedEdges orders relationships by everything that distinguishes them, so the file does not
// move because the store returned rows in another order.
func sortedEdges(in []store.GraphEdge) []store.GraphEdge {
	out := append([]store.GraphEdge(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if ra, rb := strings.Join(a.Refs, ","), strings.Join(b.Refs, ","); ra != rb {
			return ra < rb
		}
		return a.Boundary < b.Boundary
	})
	return out
}

func appendIf(attrs [][2]string, name, value string) [][2]string {
	if value == "" {
		return attrs
	}
	return append(attrs, [2]string{name, value})
}

// writeStatement writes one indented statement and its attribute list.
func writeStatement(b *strings.Builder, depth int, subject string, attrs [][2]string) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(subject)
	if len(attrs) == 0 {
		b.WriteString(";\n")
		return
	}
	b.WriteString(" [")
	for i, kv := range attrs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(kv[0])
		b.WriteByte('=')
		b.WriteString(quote(kv[1]))
	}
	b.WriteString("];\n")
}

// quote writes a DOT quoted string.
//
// `"` is the only escape the language defines inside one: `\"` becomes `"`, and everything else
// including `\\` is passed through unchanged. So a backslash is escaped only where it would
// otherwise consume the character after it -- escaping every one would put a second backslash
// into any key that contains one.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\' && (i+1 == len(s) || s[i+1] == '"'):
			b.WriteString(`\\`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
