package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// ErrNoBOMRoot reports that nothing has been declared and no root was named, so there is nothing
// for a bill of materials to be about.
//
// A distinct error rather than a message, because a caller has to be able to tell it from a
// genuine failure without matching on prose: this one means the reader has not done something
// yet, and every other one means ABOM has.
var ErrNoBOMRoot = errors.New(
	"store: a bill of materials needs a root; declare an owned scope or name one")

// ErrGroupingHasNoRepositories reports that the grouping exists as a question and holds nothing.
//
// Distinct from ErrNoBOMRoot, which means nothing has been declared at all. Collapsing the two
// sends a reader to declare a scope they have already declared.
var ErrGroupingHasNoRepositories = errors.New(
	"store: no repository in the declared scope carries that property value")

// BOM is a bill of materials: what a set of roots is composed of, what is known about each piece,
// and what was examined to produce the answer.
//
// Coverage and provenance travel inside the document rather than beside it. A reader holding only
// this file has to be able to tell an examined component from an unexamined one, and a claim
// backed by a recorded run from one that is merely believed.
type BOM struct {
	// Roots are the repositories the document is about, named so a reader can check it.
	Roots       []string       `json:"roots"`
	GeneratedAt string         `json:"generatedAt"`
	Components  []BOMComponent `json:"components"`
	// Relationships are the dependency edges between components, deduplicated.
	Relationships []BOMRelationship `json:"relationships"`
	Coverage      BOMCoverage       `json:"coverage"`
}

// BOMComponent is one thing the roots are composed of.
type BOMComponent struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Repository string `json:"repository,omitempty"`
	// Direct is relative to *this document's* roots, which is why it cannot live on the edge.
	Direct bool `json:"direct"`
	// Depth is the fewest hops from a root. Roots themselves are 0.
	Depth      int            `json:"depth"`
	References []BOMReference `json:"references"`
	Findings   []BOMFinding   `json:"findings"`
	Advisories []BOMAdvisory  `json:"advisories"`
	// Analyzed means at least one reference in use was examined, and the pair below says how
	// much. One boolean cannot distinguish partial coverage from complete.
	Analyzed     bool `json:"analyzed"`
	RefsInUse    int  `json:"refsInUse"`
	RefsAnalyzed int  `json:"refsAnalyzed"`
}

// BOMReference is a version of a component as some workflow actually wrote it.
type BOMReference struct {
	// Raw is the reference verbatim, including any owner prefix. `actions/checkout@v7` is
	// evidence about what the organization runs, not corruption to be normalized away.
	Raw   string `json:"raw,omitempty"`
	Value string `json:"value,omitempty"`
	// ResolvedSHA is empty when nothing has resolved this reference, which is different from
	// resolving it to nothing.
	ResolvedSHA string `json:"resolvedSha,omitempty"`
	Analyzed    bool   `json:"analyzed"`
}

// BOMFinding is one result, carrying whatever provenance was recorded for it.
type BOMFinding struct {
	RuleID           string  `json:"ruleId"`
	Repository       string  `json:"repository,omitempty"`
	FilePath         string  `json:"filePath,omitempty"`
	StartLine        int     `json:"startLine,omitempty"`
	Message          string  `json:"message,omitempty"`
	Severity         string  `json:"severity,omitempty"`
	SecuritySeverity float64 `json:"securitySeverity,omitempty"`
	// Band is the score read as code scanning reads it, `unknown` where the rule declares none.
	Band string `json:"band"`
	// Provider is the producer that made the claim. Empty means nobody recorded one.
	Provider string `json:"provider,omitempty"`
	// Reproducible answers whether this provider's findings can be produced again from what was
	// recorded. Nil when the provider is unknown or unrecognized, because "nobody asked" and "it
	// cannot be done" are different claims and a bare false would state the second for both.
	Reproducible *bool `json:"reproducible,omitempty"`
	// Provenance, each independently absent. Empty means not recorded, and the document says so
	// rather than implying the claim is unbacked or that it is fully attested. These three are
	// CodeQL's provenance in particular: another provider leaves them empty because that is not
	// what backs it, which is why Provider has to be read first.
	RunKey     string `json:"runKey,omitempty"`
	PackDigest string `json:"packDigest,omitempty"`
	ResultHash string `json:"resultHash,omitempty"`
	CLIVersion string `json:"cliVersion,omitempty"`
	// AnalysisModes are the shapes this defect was seen in, sorted. More than one is one defect
	// two analyses both found, never two defects: a shape is how it was looked for, not what is
	// wrong. Empty means no run claims it, which is unstated rather than regular.
	AnalysisModes []string `json:"analysisModes,omitempty"`
}

// BOMAdvisory is an advisory naming a component's repository.
//
// Reported beside the observed references, never applied to them: whether a given reference falls
// in the range is a judgment ABOM has not made.
type BOMAdvisory struct {
	GHSAID          string `json:"ghsaId"`
	Severity        string `json:"severity,omitempty"`
	Summary         string `json:"summary,omitempty"`
	VulnerableRange string `json:"vulnerableRange,omitempty"`
}

// BOMRelationship is a dependency between two components.
type BOMRelationship struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// Boundary is the stored classification, so the document and the Explorer cannot disagree
	// about what counts as a third-party dependency. Empty means unclassifiable.
	Boundary string `json:"boundary,omitempty"`
}

// BOMCoverage is what was examined to produce the document.
//
// This is what makes it a bill of materials rather than a list. Without it a reader cannot tell a
// short document from a thorough one.
type BOMCoverage struct {
	ComponentsInUse    int `json:"componentsInUse"`
	ComponentsAnalyzed int `json:"componentsAnalyzed"`
	ReferencesInUse    int `json:"referencesInUse"`
	ReferencesAnalyzed int `json:"referencesAnalyzed"`
	// FindingsUntraced is analyzed-but-unattributable: the finding exists and which run produced
	// it was not recorded. A real third state, and 2,759 findings on the reference corpus.
	Findings         int `json:"findings"`
	FindingsUntraced int `json:"findingsUntraced"`
	// FindingsWithoutHash and RunsWithoutPackDigest state how much of the provenance chain is
	// actually held, so a document cannot imply attestation it does not have.
	FindingsWithoutHash   int `json:"findingsWithoutHash"`
	FindingsWithoutDigest int `json:"findingsWithoutDigest"`
	// FindingsWithoutProvider counts findings nobody attributed to a producer. Distinct from
	// untraced: a finding can name its CodeQL run and still not say that CodeQL is what made it,
	// which is the state every finding written before providers existed is in.
	FindingsWithoutProvider int `json:"findingsWithoutProvider"`
}

// BOMOptions selects what the document is about.
type BOMOptions struct {
	// Roots defaults to the declared owned scope. Naming them explicitly is how a document for
	// one repository or one workflow is asked for.
	Roots []string
	// Properties narrows the roots to the repositories the organization says carry these key/value
	// pairs. It narrows a set rather than choosing one, so it composes with Roots instead of
	// replacing it.
	Properties []viewspec.PropertyMatch
	// NotProperties drops a root matching any of them, and beats Properties.
	NotProperties []viewspec.PropertyMatch
}

// BillOfMaterials reads everything the roots depend on, at any depth.
//
// Outward — what does this depend on — which is the opposite direction from BumpImpactOf and the
// reason neither can be reused for the other. Uncapped in depth, unlike Subgraph, because a
// closure that stops early is a neighborhood wearing a bill of materials' name.
func (s *Store) BillOfMaterials(opts BOMOptions) (BOM, error) {
	out := BOM{
		Roots:         []string{},
		Components:    []BOMComponent{},
		Relationships: []BOMRelationship{},
		GeneratedAt:   now(),
	}

	roots := opts.Roots
	if len(roots) == 0 {
		declared, err := s.OwnedRepositories()
		if err != nil {
			return out, err
		}
		roots = declared
	}
	// An undeclared scope is not an empty one. Emitting a document with no components would
	// report that the organization depends on nothing, which is a fabricated fact.
	if len(roots) == 0 {
		return out, ErrNoBOMRoot
	}
	if len(opts.Properties) > 0 || len(opts.NotProperties) > 0 {
		narrowed, err := s.narrowToProperties(roots, opts.Properties, opts.NotProperties)
		if err != nil {
			return out, err
		}
		roots = narrowed
	}
	out.Roots = append(out.Roots, roots...)
	sort.Strings(out.Roots)

	edges, err := s.bomEdges()
	if err != nil {
		return out, err
	}
	depth := walkOutward(edges, out.Roots)

	if err := s.fillComponents(&out, depth, edges); err != nil {
		return out, err
	}
	return out, nil
}

// narrowToProperties keeps the roots the organization says match, and drops the ones it says to
// exclude.
//
// A grouping holding nothing is its own error rather than ErrNoBOMRoot: telling a reader to
// declare a scope they have already declared sends them to fix the wrong thing.
func (s *Store) narrowToProperties(roots []string, include, exclude []viewspec.PropertyMatch) (
	[]string, error) {
	members, err := s.repositoriesMatching(include)
	if err != nil {
		return nil, err
	}
	dropped, err := s.repositoriesMatching(exclude)
	if err != nil {
		return nil, err
	}

	var kept []string
	for _, root := range roots {
		low := strings.ToLower(root)
		if len(include) > 0 && !members[low] {
			continue
		}
		if dropped[low] {
			continue
		}
		kept = append(kept, root)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrGroupingHasNoRepositories, describeMatches(include))
	}
	return kept, nil
}

// repositoriesWithValue names the owned repositories carrying one property value.
func (s *Store) repositoriesWithValue(name, value string) (map[string]bool, error) {
	rows, err := s.db.Query(`
SELECT r.repository_full_name
  FROM repository r
  JOIN abom_repository_property p ON p.repository_full_name = r.repository_full_name
 WHERE p.property_name = ? AND p.value = ?
   AND `+ownedFullName("r.repository_full_name"), name, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var full string
		if err := rows.Scan(&full); err != nil {
			return nil, err
		}
		out[strings.ToLower(full)] = true
	}
	return out, rows.Err()
}

// repositoriesMatching answers the repositories carrying every key asked for, at any of the values
// asked for it. Keys intersect and values of one key union, the same way the set endpoint reads
// the same term.
func (s *Store) repositoriesMatching(matches []viewspec.PropertyMatch) (map[string]bool, error) {
	names, by := valuesByKey(matches)
	var out map[string]bool
	for _, name := range names {
		perKey := map[string]bool{}
		for _, value := range by[name] {
			in, err := s.repositoriesWithValue(name, value)
			if err != nil {
				return nil, err
			}
			for repo := range in {
				perKey[repo] = true
			}
		}
		if out == nil {
			out = perKey
			continue
		}
		for repo := range out {
			if !perKey[repo] {
				delete(out, repo)
			}
		}
	}
	return out, nil
}

// describeMatches names what was asked for, so the error says which scope held nothing.
func describeMatches(matches []viewspec.PropertyMatch) string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.String())
	}
	return strings.Join(out, ", ")
}

// bomEdge is one dependency relationship as the closure walk needs it.
type bomEdge struct {
	source, target   string
	sourceRepository string
	targetRepository string
	kind             string
	boundary         string
	refRaw, refValue string
}

// bomEdges reads every dependency relationship once, with steps and jobs folded into whatever
// holds them.
//
// Folded because a step is a position in a file rather than a thing anything depends on: left in,
// they were 10,530 of 11,290 components on the reference corpus. This is the same collapse the
// Explorer draws by default, so the document and the picture agree about what a component is.
//
// Read wholesale rather than walked in SQL: a recursive CTE per root would re-read the same edges
// for every root, and the corpus is small enough that one pass and a map is both faster and
// checkable.
func (s *Store) bomEdges() ([]bomEdge, error) {
	rows, err := s.db.Query(`WITH ` + collapsedDisplay + `
		SELECT sn.node_key, dn.node_key,
		       COALESCE(sr.repository_full_name, ''),
		       COALESCE(NULLIF(dn.owner || '/' || dn.repo, '/'),
		                (SELECT tr.repository_full_name FROM repository tr
		                  WHERE tr.row_id = dn.repository_row_id), ''),
		       e.edge_kind, COALESCE(e.boundary, ''),
		       COALESCE(e.ref_raw, ''), COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN disp ds ON ds.row_id = e.src_node_row_id
		JOIN disp dd ON dd.row_id = e.dst_node_row_id
		JOIN abom_node_live sn ON sn.row_id = ds.shown
		JOIN abom_node_live dn ON dn.row_id = dd.shown
		LEFT JOIN repository sr ON sr.row_id = e.repository_row_id
		WHERE ds.shown <> dd.shown AND ` + DependencyEdgeFilter + `
		ORDER BY sn.node_key, dn.node_key, e.ref_raw`)
	if err != nil {
		return nil, fmt.Errorf("reading dependency edges: %w", err)
	}
	defer rows.Close()

	out := []bomEdge{}
	for rows.Next() {
		var e bomEdge
		if err := rows.Scan(&e.source, &e.target, &e.sourceRepository, &e.targetRepository,
			&e.kind, &e.boundary, &e.refRaw, &e.refValue); err != nil {
			return nil, fmt.Errorf("scanning a dependency edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// walkOutward finds the fewest hops from any root to each component.
//
// Repositories are the roots but components are nodes, so the first hop is "any node belonging to
// a root repository". Breadth-first, so the depth recorded is the shortest and `Direct` means
// named by the root itself rather than merely reachable in one of several ways.
func walkOutward(edges []bomEdge, roots []string) map[string]int {
	isRoot := map[string]bool{}
	for _, r := range roots {
		isRoot[strings.ToLower(r)] = true
	}

	out := map[string]int{}
	bySource := map[string][]bomEdge{}
	for _, e := range edges {
		bySource[e.source] = append(bySource[e.source], e)
	}

	frontier := []string{}
	for _, e := range edges {
		if isRoot[strings.ToLower(e.sourceRepository)] {
			if _, seen := out[e.source]; !seen {
				out[e.source] = 0
				frontier = append(frontier, e.source)
			}
		}
	}
	sort.Strings(frontier)

	for len(frontier) > 0 {
		from := frontier[0]
		frontier = frontier[1:]
		for _, e := range bySource[from] {
			if _, seen := out[e.target]; seen {
				continue
			}
			out[e.target] = out[from] + 1
			frontier = append(frontier, e.target)
		}
		// A node inside a component's own repository continues the walk at the same distance:
		// reaching `octo-org/shared` means reaching the workflows it holds, and without this the
		// walk stops at the first external name and every document is one hop deep.
		for _, e := range edges {
			if _, seen := out[e.source]; seen {
				continue
			}
			if e.sourceRepository != "" && strings.EqualFold(e.sourceRepository, from) {
				out[e.source] = out[from]
				frontier = append(frontier, e.source)
			}
		}
	}
	return out
}
