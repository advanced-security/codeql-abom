package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// fillComponents turns the walked closure into the document's components and relationships.
//
// Everything is read in whole passes keyed by node, rather than per component, because a document
// over the reference corpus has thousands of components and a query each would be thousands of
// round trips against a store held to one connection.
func (s *Store) fillComponents(out *BOM, depth map[string]int, edges []bomEdge) error {
	kinds, repositories, err := s.componentIdentity(depth)
	if err != nil {
		return err
	}
	findings, err := s.componentFindings(depth)
	if err != nil {
		return err
	}
	advisories, err := s.componentAdvisories()
	if err != nil {
		return err
	}
	coverage, err := s.componentCoverage()
	if err != nil {
		return err
	}

	refs := referencesByComponent(depth, edges, coverage)

	keys := make([]string, 0, len(depth))
	for k := range depth {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		c := BOMComponent{
			Key:        key,
			Kind:       kinds[key],
			Repository: repositories[key],
			Depth:      depth[key],
			// Depth 1 is named by a root's own file. Depth 0 is the root's own contents, which
			// are not something it depends on.
			Direct:     depth[key] == 1,
			References: []BOMReference{},
			Findings:   []BOMFinding{},
			Advisories: []BOMAdvisory{},
		}
		for _, r := range refs[key] {
			c.References = append(c.References, r)
			c.RefsInUse++
			if r.Analyzed {
				c.RefsAnalyzed++
				c.Analyzed = true
			}
		}
		sort.Slice(c.References, func(i, j int) bool {
			if c.References[i].Raw != c.References[j].Raw {
				return c.References[i].Raw < c.References[j].Raw
			}
			return c.References[i].Value < c.References[j].Value
		})
		c.Findings = append(c.Findings, findings[key]...)
		if a, ok := advisories[strings.ToLower(componentPackage(key, repositories[key]))]; ok {
			c.Advisories = append(c.Advisories, a...)
		}

		out.Components = append(out.Components, c)
	}

	out.Relationships = append(out.Relationships, relationshipsFrom(depth, edges)...)

	out.Coverage = summarizeCoverage(out.Components)
	return nil
}

// referencesByComponent collects the versions each component is named at.
//
// Keyed by the reference so a version nothing names is not invented and one named twice is not
// counted twice. An edge with either end outside the walked closure describes something this
// document does not claim to cover, and contributes nothing rather than dragging its endpoint in.
func referencesByComponent(
	depth map[string]int, edges []bomEdge, coverage map[string]refCoverage,
) map[string]map[string]BOMReference {
	refs := map[string]map[string]BOMReference{}
	for _, e := range edges {
		if _, inside := depth[e.target]; !inside {
			continue
		}
		if _, inside := depth[e.source]; !inside {
			continue
		}
		if refs[e.target] == nil {
			refs[e.target] = map[string]BOMReference{}
		}
		key := e.refRaw + "\x00" + e.refValue
		r := BOMReference{Raw: e.refRaw, Value: e.refValue}
		if c, ok := coverage[refCoverageKey(e.target, e.refValue)]; ok {
			r.ResolvedSHA, r.Analyzed = c.resolvedSHA, c.analyzed
		}
		refs[e.target][key] = r
	}
	return refs
}

// relationshipsFrom reports each distinct relationship inside the closure once, in an order that
// comes from the content rather than from map iteration, so two documents can be diffed.
//
// The kind is part of the identity: one component can both use another and depend on it for
// ordering, and collapsing those loses a distinction the graph draws.
func relationshipsFrom(depth map[string]int, edges []bomEdge) []BOMRelationship {
	out := make([]BOMRelationship, 0, len(edges))
	seen := map[[3]string]bool{}
	for _, e := range edges {
		if _, inside := depth[e.source]; !inside {
			continue
		}
		if _, inside := depth[e.target]; !inside {
			continue
		}
		k := [3]string{e.source, e.target, e.kind}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, BOMRelationship{
			Source: e.source, Target: e.target, Kind: e.kind, Boundary: e.boundary,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Kind < b.Kind
	})
	return out
}

// summarizeCoverage states what was examined, counted from the document itself so the statement
// and the contents cannot drift apart.
func summarizeCoverage(components []BOMComponent) BOMCoverage {
	var cov BOMCoverage
	for _, c := range components {
		// Only components something actually references are in the coverage denominator. A
		// workflow inside a root is part of the document but is not a dependency of it, and
		// counting it would flatter the ratio.
		if c.RefsInUse > 0 {
			cov.ComponentsInUse++
			if c.Analyzed {
				cov.ComponentsAnalyzed++
			}
		}
		cov.ReferencesInUse += c.RefsInUse
		cov.ReferencesAnalyzed += c.RefsAnalyzed
		for _, f := range c.Findings {
			cov.Findings++
			if f.RunKey == "" {
				cov.FindingsUntraced++
			}
			if f.ResultHash == "" {
				cov.FindingsWithoutHash++
			}
			if f.PackDigest == "" {
				cov.FindingsWithoutDigest++
			}
			if f.Provider == "" {
				cov.FindingsWithoutProvider++
			}
		}
	}
	return cov
}

func (s *Store) componentIdentity(depth map[string]int) (map[string]string, map[string]string, error) {
	// Only nodes that stand for themselves once steps and jobs are folded away.
	rows, err := s.db.Query(`WITH ` + collapsedDisplay + `
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(NULLIF(n.owner || '/' || n.repo, '/'), '')
		FROM abom_node_live n
		JOIN disp d ON d.row_id = n.row_id AND d.shown = n.row_id
		LEFT JOIN repository r ON r.row_id = n.repository_row_id`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading component identity: %w", err)
	}
	defer rows.Close()

	kinds, repositories := map[string]string{}, map[string]string{}
	for rows.Next() {
		var key, kind, repository, external string
		if err := rows.Scan(&key, &kind, &repository, &external); err != nil {
			return nil, nil, fmt.Errorf("scanning component identity: %w", err)
		}
		if _, inside := depth[key]; !inside {
			continue
		}
		kinds[key] = kind
		if repository == "" {
			repository = external
		}
		repositories[key] = repository
	}
	return kinds, repositories, rows.Err()
}

// componentFindings reads every finding attributed to a component in the closure, with whatever
// provenance its run recorded.
//
// Findings recorded against a step belong to the workflow once the step is folded away, or
// collapsing would drop the evidence along with the node: 1,223 components carrying findings on
// the reference corpus were steps.
//
// A LEFT JOIN to the run, not an inner one: 2,759 findings on the reference corpus have no
// query_run_id, and an inner join would silently drop every one of them. Analyzed-but-untraceable
// is a state the document reports, not one it hides.
func (s *Store) componentFindings(depth map[string]int) (map[string][]BOMFinding, error) {
	rows, err := s.db.Query(`WITH ` + collapsedDisplay + `
		SELECT shown.node_key, f.rule_id, COALESCE(fr.repository_full_name, ''),
		       COALESCE(f.file_path, ''), COALESCE(f.start_line, 0), COALESCE(f.message, ''),
		       COALESCE(f.severity, ''), COALESCE(f.security_severity, 0),
		       COALESCE(f.result_hash, ''), COALESCE(q.run_key, ''),
		       COALESCE(q.pack_digest, ''), COALESCE(q.cli_version, ''),
		       COALESCE(f.provider, ''),
		       (SELECT GROUP_CONCAT(mode) FROM
		          (SELECT DISTINCT sq.analysis_mode AS mode
		             FROM abom_finding_sighting fs
		             JOIN abom_query_run sq ON sq.row_id = fs.query_run_id
		            WHERE fs.finding_row_id = f.row_id
		            ORDER BY sq.analysis_mode))
		FROM abom_finding_node fn
		JOIN disp d ON d.row_id = fn.node_row_id
		JOIN abom_node_live shown ON shown.row_id = d.shown
		JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		JOIN repository fr ON fr.row_id = f.repository_row_id
		LEFT JOIN abom_query_run q ON q.row_id = f.query_run_id
		ORDER BY shown.node_key, f.rule_id, f.file_path, f.start_line`)
	if err != nil {
		return nil, fmt.Errorf("reading component findings: %w", err)
	}
	defer rows.Close()

	out := map[string][]BOMFinding{}
	for rows.Next() {
		var key string
		var f BOMFinding
		var modes sql.NullString
		if err := rows.Scan(&key, &f.RuleID, &f.Repository, &f.FilePath, &f.StartLine,
			&f.Message, &f.Severity, &f.SecuritySeverity, &f.ResultHash, &f.RunKey,
			&f.PackDigest, &f.CLIVersion, &f.Provider, &modes); err != nil {
			return nil, fmt.Errorf("scanning a component finding: %w", err)
		}
		f.AnalysisModes = splitSorted(modes)
		f.Band = viewspec.BandOf(&f.SecuritySeverity)
		if KnownProvider(f.Provider) {
			reproducible := Reproducible(f.Provider)
			f.Reproducible = &reproducible
		}
		if _, inside := depth[key]; !inside {
			continue
		}
		out[key] = append(out[key], f)
	}
	return out, rows.Err()
}

// componentAdvisories reads advisories by the package they name.
//
// Keyed by package rather than joined to nodes, so an advisory is reported beside a component's
// observed references without any claim that a given reference falls in the range. Deciding that
// is a judgment ABOM has not made and this document does not smuggle in.
func (s *Store) componentAdvisories() (map[string][]BOMAdvisory, error) {
	rows, err := s.db.Query(`
		SELECT p.package_name, a.ghsa_id, COALESCE(a.severity, ''), COALESCE(a.summary, ''),
		       COALESCE(p.vulnerable_version_range, '')
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		WHERE a.withdrawn_at IS NULL
		ORDER BY p.package_name, a.ghsa_id`)
	if err != nil {
		return nil, fmt.Errorf("reading advisories: %w", err)
	}
	defer rows.Close()

	out := map[string][]BOMAdvisory{}
	for rows.Next() {
		var pkg string
		var a BOMAdvisory
		if err := rows.Scan(&pkg, &a.GHSAID, &a.Severity, &a.Summary, &a.VulnerableRange); err != nil {
			return nil, fmt.Errorf("scanning an advisory: %w", err)
		}
		pkg = strings.ToLower(pkg)
		out[pkg] = append(out[pkg], a)
	}
	return out, rows.Err()
}

type refCoverage struct {
	resolvedSHA string
	analyzed    bool
}

func refCoverageKey(nodeKey, ref string) string { return nodeKey + "\x00" + ref }

// componentCoverage reads the queue, which is what knows whether a given reference was examined.
func (s *Store) componentCoverage() (map[string]refCoverage, error) {
	rows, err := s.db.Query(`
		SELECT node_key, COALESCE(ref, ''), COALESCE(resolved_sha, ''), status = 'done'
		FROM abom_frontier`)
	if err != nil {
		return nil, fmt.Errorf("reading reference coverage: %w", err)
	}
	defer rows.Close()

	out := map[string]refCoverage{}
	for rows.Next() {
		var key, ref, sha string
		var done bool
		if err := rows.Scan(&key, &ref, &sha, &done); err != nil {
			return nil, fmt.Errorf("scanning reference coverage: %w", err)
		}
		k := refCoverageKey(key, ref)
		// A reference queued more than once is analyzed if any row says so.
		prev := out[k]
		out[k] = refCoverage{
			resolvedSHA: firstNonEmpty(sha, prev.resolvedSHA),
			analyzed:    done || prev.analyzed,
		}
	}
	return out, rows.Err()
}

// componentPackage is the `owner/name` an advisory would name for this component.
func componentPackage(key, repository string) string {
	if repository != "" {
		return repository
	}
	return key
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
