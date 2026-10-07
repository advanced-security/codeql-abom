package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Advisory is a published advisory as recorded by ABOM.
//
// It is stored as an attributed external statement, keyed by its GHSA identifier, in the same
// way a CodeQL result is keyed by its rule id. Nothing here is ABOM's own judgement.
type Advisory struct {
	GHSAID         string
	CVEID          string
	Severity       string
	Summary        string
	URL            string
	CVSSv3Score    float64
	CVSSv3         string
	CVSSv4Score    float64
	CVSSv4         string
	EPSSPercentage float64
	EPSSPercentile float64
	HasEPSS        bool
	CWEs           []string
	PublishedAt    string
	UpdatedAt      string
	WithdrawnAt    string
	Packages       []AdvisoryPackage
}

// AdvisoryPackage is one affected package and range.
type AdvisoryPackage struct {
	Ecosystem       string
	Name            string
	VulnerableRange string
	FirstPatched    string
}

// AddAdvisories records advisories and the packages they affect, returning how many were
// written. Re-running refreshes them, because severity, EPSS and patched versions all change
// after publication.
func (s *Store) AddAdvisories(advisories []Advisory) (int, error) {
	if len(advisories) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	insert, err := tx.Prepare(`
		INSERT INTO abom_advisory
			(ghsa_id, cve_id, severity, summary, html_url,
			 cvss_v3_score, cvss_v3_vector, cvss_v4_score, cvss_v4_vector,
			 epss_percentage, epss_percentile, cwes,
			 published_at, updated_at, withdrawn_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ghsa_id) DO UPDATE SET
			cve_id          = excluded.cve_id,
			severity        = excluded.severity,
			summary         = excluded.summary,
			html_url        = excluded.html_url,
			cvss_v3_score   = excluded.cvss_v3_score,
			cvss_v3_vector  = excluded.cvss_v3_vector,
			cvss_v4_score   = excluded.cvss_v4_score,
			cvss_v4_vector  = excluded.cvss_v4_vector,
			epss_percentage = excluded.epss_percentage,
			epss_percentile = excluded.epss_percentile,
			cwes            = excluded.cwes,
			updated_at      = excluded.updated_at,
			withdrawn_at    = excluded.withdrawn_at,
			fetched_at      = excluded.fetched_at`)
	if err != nil {
		return 0, fmt.Errorf("preparing advisory insert: %w", err)
	}
	defer insert.Close()

	lookup, err := tx.Prepare(`SELECT row_id FROM abom_advisory WHERE ghsa_id = ?`)
	if err != nil {
		return 0, fmt.Errorf("preparing advisory lookup: %w", err)
	}
	defer lookup.Close()

	pkg, err := tx.Prepare(`
		INSERT INTO abom_advisory_package
			(advisory_row_id, ecosystem, package_name, vulnerable_version_range,
			 first_patched_version)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(advisory_row_id, package_name, vulnerable_version_range) DO UPDATE SET
			first_patched_version = excluded.first_patched_version`)
	if err != nil {
		return 0, fmt.Errorf("preparing advisory package insert: %w", err)
	}
	defer pkg.Close()

	written := 0
	for _, a := range advisories {
		if a.GHSAID == "" {
			return written, fmt.Errorf("advisory without a GHSA id")
		}
		var epssPct, epssRank any
		if a.HasEPSS {
			epssPct, epssRank = a.EPSSPercentage, a.EPSSPercentile
		}

		if _, err := insert.Exec(a.GHSAID, nullable(a.CVEID), nullable(a.Severity),
			nullable(a.Summary), nullable(a.URL),
			a.CVSSv3Score, nullable(a.CVSSv3), a.CVSSv4Score, nullable(a.CVSSv4),
			epssPct, epssRank, nullable(strings.Join(a.CWEs, ",")),
			nullable(a.PublishedAt), nullable(a.UpdatedAt), nullable(a.WithdrawnAt),
			now); err != nil {
			return written, fmt.Errorf("recording %s: %w", a.GHSAID, err)
		}

		var id int64
		if err := lookup.QueryRow(a.GHSAID).Scan(&id); err != nil {
			return written, fmt.Errorf("resolving %s: %w", a.GHSAID, err)
		}
		for _, p := range a.Packages {
			if _, err := pkg.Exec(id, nullable(p.Ecosystem), p.Name,
				p.VulnerableRange, nullable(p.FirstPatched)); err != nil {
				return written, fmt.Errorf("recording %s package %s: %w",
					a.GHSAID, p.Name, err)
			}
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// AffectedRange is one version constraint an advisory places on a package.
type AffectedRange struct {
	VulnerableRange string `json:"vulnerableRange"`
	// FirstPatched is empty when no fix has been published.
	FirstPatched string `json:"firstPatched"`
}

// NodeReach is one graph node the advisory's package covers, and what depends on it.
type NodeReach struct {
	NodeKey string `json:"nodeKey"`
	// DependentRepos comes from the graph, computed from CodeQL results, not from the advisory.
	DependentRepos int `json:"dependentRepos"`
	// References are the refs observed in the corpus for this node, most used first, verbatim.
	References []string `json:"references"`
}

// AdvisoryReach is an advisory paired with what in the graph its package touches.
//
// One advisory can constrain several ranges and cover several subpath actions, so those are
// nested rather than multiplied out: repeating the same advisory once per combination buries
// the evidence a reader needs.
type AdvisoryReach struct {
	GHSAID      string  `json:"ghsaId"`
	CVEID       string  `json:"cveId"`
	Severity    string  `json:"severity"`
	Summary     string  `json:"summary"`
	URL         string  `json:"url"`
	CVSSv3Score float64 `json:"cvssV3Score"`
	// HasCVSS distinguishes an unscored advisory from one scored zero.
	HasCVSS        bool            `json:"hasCvss"`
	EPSSPercentage float64         `json:"epssPercentage"`
	HasEPSS        bool            `json:"hasEpss"`
	PackageName    string          `json:"packageName"`
	Ranges         []AffectedRange `json:"ranges"`
	Nodes          []NodeReach     `json:"nodes"`
	// MaxDependentRepos is the widest reach among the covered nodes.
	MaxDependentRepos int `json:"maxDependentRepos"`
}

// AdvisoriesByReach lists advisories against the actions the organization actually depends on,
// widest reach first.
//
// Withdrawn advisories are excluded: GitHub has retracted them, so reporting them would attach
// a claim to an action that its own publisher no longer stands behind.
//
// No judgement is made about whether a given reference falls inside a vulnerable range. The
// ranges and the observed references are reported side by side so that decision stays with the
// reader, or with a query written to make it.
func (s *Store) AdvisoriesByReach(limit int, sc RepositoryScope) ([]AdvisoryReach, error) {
	in, err := s.reachInScope(sc, false)
	if err != nil {
		return nil, err
	}
	reach, reachArgs := in.count()
	keep, keepArgs := in.keep()
	args := append(append([]any{}, reachArgs...), keepArgs...)
	rows, err := s.db.Query(`
		SELECT a.ghsa_id, COALESCE(a.cve_id,''), COALESCE(a.severity,''),
		       COALESCE(a.summary,''), COALESCE(a.html_url,''),
		       a.cvss_v3_score, a.epss_percentage,
		       p.package_name, p.vulnerable_version_range,
		       COALESCE(p.first_patched_version,''),
		       n.node_key, `+reach+`
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		JOIN abom_node_live n
		  ON n.owner IS NOT NULL AND n.repo IS NOT NULL
		 AND n.owner || '/' || n.repo = p.package_name
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE a.withdrawn_at IS NULL`+keep+`
		ORDER BY a.ghsa_id, p.package_name, n.node_key`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading advisory reach: %w", err)
	}
	defer rows.Close()

	out, err := scanAdvisoryReach(rows)
	if err != nil {
		return nil, err
	}
	sortAdvisoryReach(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	if err := s.attachAdvisoryReachReferences(out); err != nil {
		return nil, err
	}
	return out, nil
}

type advisoryReachRow struct {
	ghsa, cve, severity, summary, url string
	pkgName, vulnRange, patched       string
	nodeKey                           string
	cvss, epss                        sql.NullFloat64
	repos                             int
}

type advisoryReachAccumulator struct {
	byKey     map[string]*AdvisoryReach
	order     []string
	seenRange map[string]bool
	seenNode  map[string]bool
}

func newAdvisoryReachAccumulator() advisoryReachAccumulator {
	return advisoryReachAccumulator{
		byKey: map[string]*AdvisoryReach{}, seenRange: map[string]bool{}, seenNode: map[string]bool{},
	}
}

func scanAdvisoryReach(rows *sql.Rows) ([]AdvisoryReach, error) {
	acc := newAdvisoryReachAccumulator()
	for rows.Next() {
		row, err := scanAdvisoryReachRow(rows)
		if err != nil {
			return nil, err
		}
		acc.add(row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return acc.list(), nil
}

func scanAdvisoryReachRow(rows *sql.Rows) (advisoryReachRow, error) {
	var row advisoryReachRow
	if err := rows.Scan(&row.ghsa, &row.cve, &row.severity, &row.summary, &row.url, &row.cvss,
		&row.epss, &row.pkgName, &row.vulnRange, &row.patched, &row.nodeKey, &row.repos); err != nil {
		return row, fmt.Errorf("scanning advisory reach: %w", err)
	}
	return row, nil
}

func (a *advisoryReachAccumulator) add(row advisoryReachRow) {
	key := row.ghsa + "\x00" + row.pkgName
	entry := a.entry(key, row)
	a.addRange(entry, key, row)
	a.addNode(entry, key, row)
}

func (a *advisoryReachAccumulator) entry(key string, row advisoryReachRow) *AdvisoryReach {
	entry, ok := a.byKey[key]
	if ok {
		return entry
	}
	entry = &AdvisoryReach{
		GHSAID: row.ghsa, CVEID: row.cve, Severity: row.severity, Summary: row.summary,
		URL: row.url, PackageName: row.pkgName,
	}
	// A zero CVSS score in the response means unscored, not harmless.
	if row.cvss.Valid && row.cvss.Float64 > 0 {
		entry.CVSSv3Score, entry.HasCVSS = row.cvss.Float64, true
	}
	if row.epss.Valid {
		entry.EPSSPercentage, entry.HasEPSS = row.epss.Float64, true
	}
	a.byKey[key] = entry
	a.order = append(a.order, key)
	return entry
}

func (a *advisoryReachAccumulator) addRange(entry *AdvisoryReach, key string, row advisoryReachRow) {
	if rk := key + "\x00" + row.vulnRange; !a.seenRange[rk] {
		a.seenRange[rk] = true
		entry.Ranges = append(entry.Ranges,
			AffectedRange{VulnerableRange: row.vulnRange, FirstPatched: row.patched})
	}
}

func (a *advisoryReachAccumulator) addNode(entry *AdvisoryReach, key string, row advisoryReachRow) {
	if nk := key + "\x00" + row.nodeKey; !a.seenNode[nk] {
		a.seenNode[nk] = true
		entry.Nodes = append(entry.Nodes,
			NodeReach{NodeKey: row.nodeKey, DependentRepos: row.repos})
		if row.repos > entry.MaxDependentRepos {
			entry.MaxDependentRepos = row.repos
		}
	}
}

func (a *advisoryReachAccumulator) list() []AdvisoryReach {
	out := make([]AdvisoryReach, 0, len(a.order))
	for _, key := range a.order {
		out = append(out, *a.byKey[key])
	}
	return out
}

func sortAdvisoryReach(out []AdvisoryReach) {
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].MaxDependentRepos > out[j].MaxDependentRepos
	})
}

func (s *Store) attachAdvisoryReachReferences(out []AdvisoryReach) error {
	for i := range out {
		sort.SliceStable(out[i].Nodes, func(a, b int) bool {
			return out[i].Nodes[a].DependentRepos > out[i].Nodes[b].DependentRepos
		})
		for j := range out[i].Nodes {
			refs, err := s.referencesTo(out[i].Nodes[j].NodeKey, AnalysisModeRegular)
			if err != nil {
				return err
			}
			out[i].Nodes[j].References = refs
		}
	}
	return nil
}

// referencesTo returns the distinct references the corpus uses for a node, with how often each
// appears. These are the evidence a reader needs to decide whether a range applies.
func (s *Store) referencesTo(nodeKey, mode string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(e.ref_value, ''), COUNT(*)
		FROM `+edgeSource(mode)+` e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE n.node_key = ? AND r.origin = ?
		GROUP BY e.ref_value
		ORDER BY COUNT(*) DESC`, nodeKey, OriginSeed)
	if err != nil {
		return nil, fmt.Errorf("reading references to %q: %w", nodeKey, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ref string
		var n int
		if err := rows.Scan(&ref, &n); err != nil {
			return nil, err
		}
		if ref == "" {
			ref = "(no ref)"
		}
		out = append(out, fmt.Sprintf("%s (%d)", ref, n))
	}
	return out, rows.Err()
}

// Exposure is one repository's route to an advisory-affected action.
type Exposure struct {
	Repository string `json:"repository"`
	GHSAID     string `json:"ghsaId"`
	CVEID      string `json:"cveId"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
	NodeKey    string `json:"nodeKey"`
	// Hops of 1 means the repository's own workflow uses the affected action.
	Hops int      `json:"hops"`
	Path []string `json:"path"`
	// References are the refs the repository itself uses, empty for a transitive route where
	// the repository never names the action.
	References []string `json:"references"`
}

// ExposuresByRepository lists how repositories under review reach affected actions.
//
// Direct routes come first within a repository: they are the ones the repository can fix
// itself, whereas a transitive route waits on an intermediary that may belong to someone else.
func (s *Store) ExposuresByRepository(limit int, sc RepositoryScope) ([]Exposure, error) {
	bound, bindings, err := s.restrict(sc, "r.repository_full_name")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT r.repository_full_name, e.ghsa_id, COALESCE(a.cve_id,''),
		       COALESCE(a.severity,''), COALESCE(a.summary,''),
		       n.node_key, e.hops, e.path
		FROM abom_exposure e
		JOIN repository r ON r.row_id = e.repository_row_id
		JOIN abom_node_live n ON n.row_id = e.node_row_id
		JOIN abom_advisory a ON a.ghsa_id = e.ghsa_id
		WHERE 1`+bound+`
		ORDER BY e.hops ASC, r.repository_full_name, e.ghsa_id`, bindings...)
	if err != nil {
		return nil, fmt.Errorf("reading exposures: %w", err)
	}
	defer rows.Close()

	var out = []Exposure{}
	for rows.Next() {
		var e Exposure
		var raw string
		if err := rows.Scan(&e.Repository, &e.GHSAID, &e.CVEID, &e.Severity, &e.Summary,
			&e.NodeKey, &e.Hops, &raw); err != nil {
			return nil, fmt.Errorf("scanning exposure: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &e.Path); err != nil {
			return nil, fmt.Errorf("decoding exposure path: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		if out[i].Hops != 1 {
			continue
		}
		refs, err := s.referencesFrom(out[i].Repository, out[i].NodeKey)
		if err != nil {
			return nil, err
		}
		out[i].References = refs
	}
	return out, nil
}

// referencesFrom returns the references one repository uses for a node.
func (s *Store) referencesFrom(repository, nodeKey string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE n.node_key = ? AND r.repository_full_name = ?
		ORDER BY 1`, nodeKey, repository)
	if err != nil {
		return nil, fmt.Errorf("reading references from %q: %w", repository, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		if ref == "" {
			ref = "(no ref)"
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// ExposureSummary counts, per advisory, how many repositories reach it and by what route.
type ExposureSummary struct {
	GHSAID     string `json:"ghsaId"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
	Direct     int    `json:"direct"`
	Transitive int    `json:"transitive"`
}

// ExposureByAdvisory summarizes reach per advisory, widest first.
//
// Direct and transitive are kept apart because they call for different work: one is a version
// bump in the affected repository, the other is a fix somewhere upstream of it.
//
// A row here is an advisory and its numbers count repositories, so the scope restricts the rows
// being counted rather than the rows being returned — see `restrict`.
func (s *Store) ExposureByAdvisory(sc RepositoryScope) ([]ExposureSummary, error) {
	bound, bindings, err := s.restrict(sc, "er.repository_full_name")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT e.ghsa_id, COALESCE(a.severity,''), COALESCE(a.summary,''),
		       COUNT(DISTINCT CASE WHEN e.hops = 1 THEN e.repository_row_id END),
		       COUNT(DISTINCT CASE WHEN e.hops > 1 THEN e.repository_row_id END)
		FROM abom_exposure e
		JOIN abom_advisory a ON a.ghsa_id = e.ghsa_id
		JOIN repository er ON er.row_id = e.repository_row_id`+bound+`
		GROUP BY e.ghsa_id, a.severity, a.summary
		ORDER BY 4 + 5 DESC, e.ghsa_id`, bindings...)
	if err != nil {
		return nil, fmt.Errorf("summarizing exposure: %w", err)
	}
	defer rows.Close()

	out := []ExposureSummary{}
	for rows.Next() {
		var e ExposureSummary
		if err := rows.Scan(&e.GHSAID, &e.Severity, &e.Summary, &e.Direct, &e.Transitive); err != nil {
			return nil, fmt.Errorf("scanning exposure summary: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdvisoriesFetchedAt reports when advisories were last collected, and false if never.
//
// A crawl that has drained never ingests again, so a refresh keyed only on arrival would leave
// this frozen; a caller enforcing an age ceiling needs to seed it from what is already held
// rather than re-fetch on every restart.
func (s *Store) AdvisoriesFetchedAt() (time.Time, bool, error) {
	var fetched sql.NullString
	if err := s.db.QueryRow(`SELECT MAX(fetched_at) FROM abom_advisory`).Scan(&fetched); err != nil {
		return time.Time{}, false, fmt.Errorf("reading advisory age: %w", err)
	}
	if !fetched.Valid || fetched.String == "" {
		return time.Time{}, false, nil
	}
	at, err := time.Parse(time.RFC3339, fetched.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing advisory age %q: %w", fetched.String, err)
	}
	return at, true, nil
}

// AdvisoryPackages returns the repository-scoped package names worth querying: every external
// action the organization depends on.
//
// Names are repository-scoped because that is the only granularity the advisory API knows;
// subpath actions collapse onto their repository.
func (s *Store) AdvisoryPackages() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT n.owner || '/' || n.repo
		FROM abom_node_live n
		WHERE n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		  AND n.node_kind IN ('action', 'composite_action', 'reusable_workflow', 'workflow')
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("listing advisory packages: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
