package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The lifecycle a query version moves through. ABOM computes the evidence; a person advances the
// stage, so these are recorded acts rather than states anything derives.
const (
	StageDev          = "dev"
	StageTest         = "test"
	StageExtendedTest = "extended-test"
	StageReview       = "review"
	StageDeploy       = "deploy"
)

// Where a query came from, read from the pack that supplied it.
//
// Named apart from the repository origins in graph.go on purpose: that one records how a row
// entered the corpus, this one records who wrote a query. Sharing a vocabulary would invite
// joining two things that answer different questions.
const (
	QueryOriginUpstream = "upstream"
	QueryOriginAbom     = "abom"
	QueryOriginUser     = "user"
	// QueryOriginMixed is a lane that ran rules from several packs at once, which the findings
	// lane does: `database analyze` takes them in one invocation, so the run names all of them
	// and no one of them is where the results came from.
	QueryOriginMixed = "mixed"
)

// Stages is the lifecycle in order, which is also the order a reader should be offered it.
func Stages() []string {
	return []string{StageDev, StageTest, StageExtendedTest, StageReview, StageDeploy}
}

// ValidStage reports whether a stage is one this build knows how to read.
func ValidStage(stage string) bool {
	for _, s := range Stages() {
		if s == stage {
			return true
		}
	}
	return false
}

// CatalogQuery is one version of one query, and what is known about it.
type CatalogQuery struct {
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
	Pack         string `json:"pack"`
	PackVersion  string `json:"packVersion"`
	CLIVersion   string `json:"cliVersion"`
	Origin       string `json:"origin"`
	// Suite is set when what ran was a whole suite rather than one query, which is how the
	// findings lane records itself: `database analyze` takes the suite in one invocation.
	Suite string `json:"suite"`
	// Kind is the query's `@kind`, empty for a table query. KindRecorded is what keeps that
	// emptiness apart from a query nobody resolved a suite for -- absent is not "table query".
	Kind         string `json:"kind"`
	KindRecorded bool   `json:"kindRecorded"`
	// Discovery marks a query that builds the graph rather than reporting a finding. It is read
	// from the recorded kind, never from the rule id: a table query yields BQRS and no SARIF.
	Discovery bool `json:"discovery"`
	// Lane marks a row that is a whole-suite invocation rather than a query. `database analyze`
	// takes a suite in one call, so the ledger records the suite as what ran -- which is true, and
	// is not a query anybody develops or versions.
	Lane bool `json:"lane"`
	// Stage is empty when nobody has advanced this version. Absent is not `dev`: a default would
	// report a decision nobody made.
	Stage    string `json:"stage"`
	StagedBy string `json:"stagedBy"`
	StagedAt string `json:"stagedAt"`
	// Runs and Repositories are the evidence a readiness decision rests on. They differ: one
	// query against two repositories is two runs and two repositories, and twice against one is
	// two runs and one repository.
	Runs         int    `json:"runs"`
	Repositories int    `json:"repositories"`
	FirstRunAt   string `json:"firstRunAt"`
	LastRunAt    string `json:"lastRunAt"`
	// Analyses counts the runs somebody asked for against *this version*, which is what ties a
	// request to the thing it was testing.
	Analyses     int    `json:"analyses"`
	LastAnalysis string `json:"lastAnalysis"`
	// Tested says whether anything has run this version's tests, and Passing what they said.
	// Never-tested is its own answer: a version nobody has tested is not one whose tests failed,
	// and only the second is a defect to go and look at.
	Tested     bool   `json:"tested"`
	Passing    bool   `json:"passing"`
	TestDetail string `json:"testDetail"`
	// Working marks the version on disk in the operator's pack now: the only one whose tests can
	// be run, and the one the authoring steps describe.
	Working bool `json:"working"`
}

// WorkingQuery is a query as it is on disk in the operator's pack, which the store does not read
// for itself.
type WorkingQuery struct {
	QueryID, QueryVersion, Pack, Kind string
}

// QueryCatalog reports every query a suite has resolved to, identified by what identifies a
// query: its CodeQL `@id`, the version of the pack that supplied it, and the content hash of the
// `.ql` and the libraries it imports.
//
// Built over `abom_suite_query` rather than over the run ledger. The ledger's unit is an
// *evaluation*, and the findings lane evaluates a whole suite in one `database analyze`
// invocation — so its rows are keyed by the suite reference and an individual query like
// `actions/unpinned-tag` never appears as itself however many times it has run. A catalog keyed
// that way shows a pack-and-suite string where a reader is looking for a rule.
//
// Run counts are then attributed back: a ledger row naming the query is that query's, and a row
// naming a suite belongs to every query the suite resolved to. Joined in Go rather than in SQL
// because the alternative is a correlated subquery over every run for every query.
func (s *Store) QueryCatalog(working ...WorkingQuery) ([]CatalogQuery, error) {
	spine, err := s.catalogSpine()
	if err != nil {
		return nil, err
	}
	ledger, suites, versioned, err := s.ledgerStats()
	if err != nil {
		return nil, err
	}
	stages, err := s.stageRows()
	if err != nil {
		return nil, err
	}
	analyses, err := s.analysisCounts()
	if err != nil {
		return nil, err
	}
	tests, err := s.queryTests()
	if err != nil {
		return nil, err
	}
	// What was decided about a version and what its tests said, whichever way it was listed.
	attach := func(q *CatalogQuery, key string) {
		if st, ok := stages[key]; ok {
			q.Stage, q.StagedBy, q.StagedAt = st.stage, st.by, st.at
		}
		q.Analyses = analyses[key]
		if t, ok := tests[key]; ok {
			q.Tested, q.Passing, q.TestDetail = true, t.Passed, t.Detail
		}
	}

	out := make([]CatalogQuery, 0, len(spine))
	covered := map[string]bool{}
	for _, q := range spine {
		stats := ledger[q.QueryID]
		for _, suite := range q.suites {
			stats.merge(suites[suite])
		}
		q.Runs, q.Repositories = stats.runs, len(stats.repositories)
		q.FirstRunAt, q.LastRunAt = stats.first, stats.last
		key := q.QueryID + "\x00" + q.QueryVersion
		attach(&q.CatalogQuery, key)
		q.Origin = originOfPack(q.Pack)
		q.KindRecorded = true
		q.Discovery = q.Kind == ""
		covered[key] = true
		out = append(out, q.CatalogQuery)
	}

	// What each query id was recorded as, so a version too old to have been resolved still knows
	// what kind of query it is.
	kinds := map[string]string{}
	for _, q := range spine {
		kinds[q.QueryID] = q.Kind
	}
	for _, w := range working {
		if _, recorded := kinds[w.QueryID]; !recorded && w.Kind != "" {
			kinds[w.QueryID] = w.Kind
		}
	}

	// A ledger row no suite resolution accounts for is still something that ran. Dropping it
	// would understate what the corpus has been analyzed with, and the rows most likely to be
	// here are the oldest ones -- written before suite contents were recorded at all.
	for key, stats := range versioned {
		id, version, _ := strings.Cut(key, "\x00")
		if covered[key] {
			continue
		}
		q := CatalogQuery{
			QueryID: id, QueryVersion: version,
			Pack: stats.pack, PackVersion: stats.packVersion, CLIVersion: stats.cliVersion,
			Runs: stats.runs, Repositories: len(stats.repositories),
			FirstRunAt: stats.first, LastRunAt: stats.last,
		}
		q.Origin = originOfPack(q.Pack)
		// A lane row names what it invoked, which is a suite. Kept apart from the query column so
		// a reader is not shown a pack-and-suite string where a rule belongs.
		q.Suite, q.QueryID = splitSuite(q.QueryID)
		// A query id holds no whitespace; one that does names several queries run as one evaluation.
		q.Lane = q.Suite != "" || strings.ContainsAny(q.QueryID, " \t")
		// A query's kind does not vary with its version: if it did, it would be a different query.
		// So a version too old to have been resolved still inherits what the id was recorded as,
		// which is what keeps one query from being half hidden and half listed.
		if kind, ok := kinds[q.QueryID]; ok && !q.Lane {
			q.Kind, q.KindRecorded = kind, true
			q.Discovery = kind == ""
		}
		attach(&q, key)
		out = append(out, q)
	}
	out = withWorkingCopy(out, working, attach)
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueryID != out[j].QueryID {
			return out[i].QueryID < out[j].QueryID
		}
		return out[i].QueryVersion < out[j].QueryVersion
	})
	return out, nil
}

// withWorkingCopy marks the versions on disk now, and lists the ones nothing has run yet.
//
// A query is written, tested and staged before anything runs it, so a catalog built only from
// what ran would show it for the first time after its first run.
func withWorkingCopy(
	out []CatalogQuery, working []WorkingQuery, attach func(*CatalogQuery, string),
) []CatalogQuery {
	listed := make(map[string]int, len(out))
	for i, q := range out {
		listed[q.QueryID+"\x00"+q.QueryVersion] = i
	}
	for _, w := range working {
		key := w.QueryID + "\x00" + w.QueryVersion
		i, ok := listed[key]
		if !ok {
			q := CatalogQuery{QueryID: w.QueryID, QueryVersion: w.QueryVersion, Pack: w.Pack,
				Origin: originOfPack(w.Pack)}
			attach(&q, key)
			out = append(out, q)
			i = len(out) - 1
			listed[key] = i
		}
		out[i].Working = true
		// The file is this version, so what it declares is what the version is.
		if w.Kind != "" {
			out[i].Kind, out[i].KindRecorded, out[i].Discovery = w.Kind, true, false
		}
	}
	return out
}

// splitSuite separates a suite reference from a query id.
//
// Only a ledger-only row needs this: the findings lane records what it invoked, which is a suite,
// and one column holding both leaves a reader unable to tell a rule from the set it arrived in.
// A row with suite contents behind it already has the query's own id.
func splitSuite(id string) (suite, query string) {
	if !strings.Contains(id, ".qls") {
		return "", id
	}
	names := strings.Fields(id)
	short := make([]string, 0, len(names))
	for _, name := range names {
		if _, file, found := strings.Cut(name, "codeql-suites/"); found {
			short = append(short, file)
			continue
		}
		short = append(short, name)
	}
	return id, strings.Join(short, " ")
}

// spineQuery is a catalog row and the suites it was reached through.
type spineQuery struct {
	CatalogQuery
	suites []string
}

// catalogSpine is every query a suite resolved to, one row per identity.
func (s *Store) catalogSpine() ([]spineQuery, error) {
	rows, err := s.db.Query(`
		SELECT query_id, COALESCE(query_version, ''), pack, COALESCE(pack_version, ''),
		       COALESCE(kind, ''), suite
		FROM abom_suite_query
		ORDER BY query_id, query_version, suite`)
	if err != nil {
		return nil, fmt.Errorf("reading the query catalog: %w", err)
	}
	defer rows.Close()

	by := map[string]*spineQuery{}
	order := []string{}
	for rows.Next() {
		var q CatalogQuery
		var suite string
		if err := rows.Scan(&q.QueryID, &q.QueryVersion, &q.Pack, &q.PackVersion,
			&q.Kind, &suite); err != nil {
			return nil, fmt.Errorf("scanning a catalog entry: %w", err)
		}
		// One query in two suites is one catalog row: the identity is the query, and which suite
		// selected it is how it was reached rather than what it is.
		key := q.QueryID + "\x00" + q.QueryVersion + "\x00" + q.PackVersion
		entry, seen := by[key]
		if !seen {
			entry = &spineQuery{CatalogQuery: q}
			by[key] = entry
			order = append(order, key)
		}
		entry.suites = append(entry.suites, suite)
		if entry.Suite == "" {
			entry.Suite = suite
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the query catalog: %w", err)
	}

	spine := make([]spineQuery, 0, len(order))
	for _, key := range order {
		spine = append(spine, *by[key])
	}
	return spine, nil
}

// runStats is what the ledger knows about one query or one suite.
type runStats struct {
	runs         int
	repositories map[string]bool
	first, last  string
	// What the ledger recorded alongside. Only a row no suite resolution accounts for needs
	// these: everything else takes its identity from the suite contents, which are per query.
	pack, packVersion, cliVersion string
}

func (r *runStats) merge(other runStats) {
	r.runs += other.runs
	if r.repositories == nil {
		r.repositories = map[string]bool{}
	}
	for repo := range other.repositories {
		r.repositories[repo] = true
	}
	if other.first != "" && (r.first == "" || other.first < r.first) {
		r.first = other.first
	}
	if other.last > r.last {
		r.last = other.last
	}
}

// ledgerStats reads what has run: by query id, by the suite a lane named, and by the id and
// version together -- the last for rows no suite resolution accounts for, which keep whatever
// version the ledger recorded.
func (s *Store) ledgerStats() (
	byQuery, bySuite, byVersion map[string]runStats, err error,
) {
	byQuery, bySuite, byVersion = map[string]runStats{}, map[string]runStats{}, map[string]runStats{}
	rows, err := s.db.Query(`
		SELECT query_id, COALESCE(query_version, ''), repository, ran_at,
		       COALESCE(pack, ''), COALESCE(pack_version, ''), COALESCE(cli_version, '')
		FROM abom_query_run`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading the run ledger: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, version, repository, ranAt string
		var pack, packVersion, cliVersion string
		if err := rows.Scan(&id, &version, &repository, &ranAt,
			&pack, &packVersion, &cliVersion); err != nil {
			return nil, nil, nil, fmt.Errorf("scanning a ledger row: %w", err)
		}
		key := id + "\x00" + version
		noteRun(byVersion, key, repository, ranAt)
		if stats := byVersion[key]; stats.pack == "" {
			stats.pack, stats.packVersion, stats.cliVersion = pack, packVersion, cliVersion
			byVersion[key] = stats
		}
		// A findings-lane row names the suites it invoked, space-separated, rather than a query.
		if strings.Contains(id, ".qls") {
			for _, suite := range strings.Fields(id) {
				noteRun(bySuite, suite, repository, ranAt)
			}
			continue
		}
		// An on-demand run of several queries names each of them, and each of them ran.
		for _, query := range strings.Fields(id) {
			noteRun(byQuery, query, repository, ranAt)
		}
	}
	return byQuery, bySuite, byVersion, rows.Err()
}

func noteRun(into map[string]runStats, key, repository, ranAt string) {
	stats := into[key]
	if stats.repositories == nil {
		stats.repositories = map[string]bool{}
	}
	stats.runs++
	stats.repositories[repository] = true
	if stats.first == "" || ranAt < stats.first {
		stats.first = ranAt
	}
	if ranAt > stats.last {
		stats.last = ranAt
	}
	into[key] = stats
}

type stageRow struct{ stage, by, at string }

func (s *Store) stageRows() (map[string]stageRow, error) {
	out := map[string]stageRow{}
	rows, err := s.db.Query(
		`SELECT query_id, query_version, stage, advanced_by, advanced_at FROM abom_query_stage`)
	if err != nil {
		return nil, fmt.Errorf("reading query stages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, version string
		var st stageRow
		if err := rows.Scan(&id, &version, &st.stage, &st.by, &st.at); err != nil {
			return nil, fmt.Errorf("scanning a stage: %w", err)
		}
		out[id+"\x00"+version] = st
	}
	return out, rows.Err()
}

func (s *Store) analysisCounts() (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.Query(
		`SELECT query_id, query_version, COUNT(*) FROM abom_analysis_query
		  GROUP BY query_id, query_version`)
	if err != nil {
		return nil, fmt.Errorf("counting analyses: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, version string
		var n int
		if err := rows.Scan(&id, &version, &n); err != nil {
			return nil, fmt.Errorf("scanning an analysis count: %w", err)
		}
		out[id+"\x00"+version] = n
	}
	return out, rows.Err()
}

// AdvanceQueryStage records that somebody moved a version to a stage.
//
// Who and when are part of the row because readiness is computed and advancement is not: a stage
// that appeared on its own could not be told apart from one a person signed.
func (s *Store) AdvanceQueryStage(queryID, version, stage, by string) error {
	switch {
	case queryID == "":
		return fmt.Errorf("advancing a query stage: no query named")
	case !ValidStage(stage):
		return fmt.Errorf("advancing %s: %q is not a lifecycle stage", queryID, stage)
	case by == "":
		return fmt.Errorf("advancing %s: no one is recorded as advancing it", queryID)
	}
	// The one rung that is earned rather than pressed. A ladder every step of which can be set by
	// hand is an audit trail of intentions, and the first reader to treat one as a guarantee is
	// misled by something this project built.
	if stage == StageTest {
		test, known, err := s.QueryTestFor(queryID, version)
		switch {
		case err != nil:
			return err
		case !known:
			return fmt.Errorf(
				"advancing %s to %s: nothing has run its tests, so nothing has been shown to work",
				queryID, stage)
		case !test.Passed:
			return fmt.Errorf("advancing %s to %s: its tests do not pass (%s)",
				queryID, stage, test.Detail)
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_query_stage (query_id, query_version, stage, advanced_by, advanced_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (query_id, query_version) DO UPDATE SET
			stage = excluded.stage,
			advanced_by = excluded.advanced_by,
			advanced_at = excluded.advanced_at`,
		queryID, version, stage, by, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("advancing %s@%s to %s: %w", queryID, version, stage, err)
	}
	return nil
}

// DeployedQueryIDs lists the query ids somebody has advanced to the deploy stage.
//
// Ids rather than versions: a suite selects by id, and which version of it runs is decided by
// what is on disk when the round resolves. Deploying says *this query* should run from now on,
// and the version that answers is whatever the pack holds then.
func (s *Store) DeployedQueryIDs() ([]string, error) {
	out := []string{}
	rows, err := s.db.Query(
		`SELECT DISTINCT query_id FROM abom_query_stage WHERE stage = ? ORDER BY query_id`,
		StageDeploy)
	if err != nil {
		return nil, fmt.Errorf("reading deployed queries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning a deployed query: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// originOfPack reads provenance from the pack that supplied a query.
//
// Never from the rule id: `abom/` is a naming convention anybody can adopt, and a query's prefix
// says what its author called it rather than where it came from. An unrecognized pack is the
// operator's own, which is the reading that does not claim more than is known -- ABOM's packs and
// the upstream ones are the two this build can name with certainty.
func originOfPack(pack string) string {
	switch {
	case pack == "":
		return ""
	case strings.Contains(pack, " "):
		// Several packs joined by identifyFindings. Measured on the crawled organization: the
		// findings lane records `codeql/actions-queries advanced-security/abom-actions-findings-src`,
		// and reading the first one called it upstream -- a suite ABOM wrote, attributed to
		// somebody else, because whichever pack sorted first decided.
		return QueryOriginMixed
	case strings.HasPrefix(pack, "codeql/"):
		return QueryOriginUpstream
	case strings.HasPrefix(pack, "advanced-security/abom-"):
		return QueryOriginAbom
	default:
		return QueryOriginUser
	}
}
