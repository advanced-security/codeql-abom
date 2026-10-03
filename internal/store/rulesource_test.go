package store

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	upstreamFindings = "codeql/actions-queries"
	abomFindings     = "advanced-security/abom-actions-findings-src"
	customFindings   = "abom/custom-queries"
)

func sourcedRun(t *testing.T, s *Store, q QueryRun) int64 {
	t.Helper()
	q.AnalysisMode = AnalysisModeRegular
	id, err := s.RecordQueryRun(q)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	return id
}

// sourcedStore is the match fixture's two repositories after the crawl's findings lane ran an
// upstream rule and one of ABOM's over octo-org/app, and a run of the operator's own query over
// octo-org/tool. The second finding in octo-org/tool predates evaluations keeping their rules.
func sourcedStore(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)
	lane := sourcedRun(t, s, QueryRun{
		QueryID: "abom-findings.qls", QueryVersion: "lane-v1", Pack: upstreamFindings + " " + abomFindings,
		Repository: "octo-org/app", DBCommit: "c-app", CLIVersion: "2.27.1", RecordedBy: RecordedByCrawl,
		Rules: []QueryRunRule{
			{RuleID: "actions/unpinned-tag", QueryVersion: "u1", Pack: upstreamFindings,
				PackVersion: "0.6.36", PackDigest: "d-up"},
			{RuleID: "abom/secrets-inherit", QueryVersion: "a1", Pack: abomFindings,
				PackVersion: "2.27.1", PackDigest: "d-abom"},
		},
	})
	own := sourcedRun(t, s, QueryRun{
		QueryID: "acme/checkout-tag-pin", QueryVersion: "c1", Pack: customFindings,
		Repository: "octo-org/tool", DBCommit: "c-tool", RecordedBy: RecordedByRun,
		Rules: []QueryRunRule{{RuleID: "acme/checkout-tag-pin", QueryVersion: "c1", Pack: customFindings,
			PackVersion: "0.0.1", PackDigest: "d-acme"}},
	})
	seedFindings(t, s, repoID(t, s, "octo-org/app"), ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", QueryRunID: lane, Provider: "CodeQL"},
		Finding{RuleID: "abom/secrets-inherit", QueryRunID: lane, Provider: "CodeQL"},
	)
	seedFindings(t, s, repoID(t, s, "octo-org/tool"), ".github/workflows/ci.yml",
		Finding{RuleID: "acme/checkout-tag-pin", QueryRunID: own, Provider: "CodeQL"},
		Finding{RuleID: "actions/unpinned-tag", Provider: "CodeQL"},
	)
	return s
}

// reportAgainFromCustom has the operator's pack report octo-org/app's unpinned-tag finding again
// through its own copy of the rule, at the location the lane reported it.
func reportAgainFromCustom(t *testing.T, s *Store) int64 {
	t.Helper()
	again := sourcedRun(t, s, QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "x1", Pack: customFindings,
		Repository: "octo-org/app", DBCommit: "c-app", RecordedBy: RecordedByRun,
		Rules: []QueryRunRule{{RuleID: "actions/unpinned-tag", QueryVersion: "x1", Pack: customFindings}},
	})
	seedFindings(t, s, repoID(t, s, "octo-org/app"), ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", QueryRunID: again, Provider: "CodeQL"})
	return again
}

func findingRow(t *testing.T, s *Store, repository, rule string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`
		SELECT f.row_id FROM abom_finding f JOIN repository r ON r.row_id = f.repository_row_id
		WHERE r.repository_full_name = ? AND f.rule_id = ?`, repository, rule).Scan(&id); err != nil {
		t.Fatalf("finding %s in %s: %v", rule, repository, err)
	}
	return id
}

func matchedRules(t *testing.T, s *Store, sel FindingSelector) (int, []string) {
	t.Helper()
	got, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	rules := []string{}
	for _, n := range got.Nodes {
		rules = append(rules, n.Rules...)
	}
	sort.Strings(rules)
	return got.Findings, rules
}

// A finding's source is where its rule's query came from, as the evaluation that reported it
// recorded. Read from the evaluation's packs instead, a lane that ran rules from two packs could
// only ever answer "mixed", and the rule id is a naming convention anybody can adopt.
func TestAFindingIsSelectedByWhereItsRulesQueryCameFrom(t *testing.T) {
	s := sourcedStore(t)
	for source, want := range map[string][]string{
		QuerySourceUpstream: {"actions/unpinned-tag"},
		QuerySourceAbom:     {"abom/secrets-inherit"},
		QuerySourceCustom:   {"acme/checkout-tag-pin"},
		QuerySourceMixed:    {},
	} {
		findings, rules := matchedRules(t, s, FindingSelector{Sources: []string{source}})
		if findings != len(want) || !reflect.DeepEqual(rules, want) {
			t.Errorf("source=%s matched %d findings of %v, want %v", source, findings, rules, want)
		}
	}

	// The unpinned-tag finding in octo-org/tool predates evaluations keeping their rules. Unknown
	// is not a match, so every source together still leaves it out.
	all, _ := matchedRules(t, s, FindingSelector{Sources: []string{
		QuerySourceUpstream, QuerySourceAbom, QuerySourceCustom, QuerySourceMixed}})
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if all != 3 || total != 4 {
		t.Errorf("every source together matched %d of %d findings, want the 3 with a recorded source", all, total)
	}
}

// A source narrows what the other terms chose, as every finding term does: "upstream rules under
// actions/*" is one question, not two answers added together.
func TestASourceNarrowsTheRulesAskedFor(t *testing.T) {
	s := sourcedStore(t)
	if n, rules := matchedRules(t, s, FindingSelector{
		Rules: []string{"actions/*"}, Sources: []string{QuerySourceAbom}}); n != 0 {
		t.Errorf("ABOM's rules under actions/* matched %d findings of %v, want none", n, rules)
	}
	if n, _ := matchedRules(t, s, FindingSelector{
		Rules: []string{"actions/*"}, Sources: []string{QuerySourceUpstream}}); n != 1 {
		t.Errorf("upstream rules under actions/* matched %d findings, want 1", n)
	}
}

// A source answers only for findings whose evaluation recorded one, so an answer narrowed by source
// says how many findings every other term matched and no source could be named for. On the crawled
// organization that is every one of ABOM's own rules, all run in a lane of two packs before rules
// were kept, and a bare zero would read as "ABOM's rules found nothing".
func TestASourceSaysHowManyFindingsItCouldNotAnswerFor(t *testing.T) {
	s := sourcedStore(t)
	for _, c := range []struct {
		sel       FindingSelector
		unsourced int
	}{
		{FindingSelector{Sources: []string{QuerySourceAbom}}, 1},
		{FindingSelector{Rules: []string{"acme/*"}, Sources: []string{QuerySourceCustom}}, 0},
		{FindingSelector{Rules: []string{"actions/*"}}, 0},
	} {
		got, err := s.NodesMatching(c.sel)
		if err != nil {
			t.Fatalf("NodesMatching: %v", err)
		}
		if got.Unsourced != c.unsourced {
			t.Errorf("%+v says %d findings have no recorded source, want %d", c.sel, got.Unsourced, c.unsourced)
		}
	}
}

// One defect reported through two copies of a rule is one finding with two sources. The finding
// row names only its latest evaluation, so a source read from that alone would drop the upstream
// lane the moment the operator's pack reported the same result.
func TestAFindingReportedFromTwoSourcesAnswersToBoth(t *testing.T) {
	s := sourcedStore(t)
	reportAgainFromCustom(t, s)

	if n, _ := matchedRules(t, s, FindingSelector{Sources: []string{QuerySourceUpstream}}); n != 1 {
		t.Errorf("source=upstream matched %d findings after the custom pack reported one again, want 1", n)
	}
	n, rules := matchedRules(t, s, FindingSelector{Sources: []string{QuerySourceCustom}})
	if want := []string{"acme/checkout-tag-pin", "actions/unpinned-tag"}; n != 2 || !reflect.DeepEqual(rules, want) {
		t.Errorf("source=custom matched %d findings of %v, want %v", n, rules, want)
	}
}

// The listing narrows by source with the set path's meaning. One predicate selecting different
// findings on two surfaces is the divergence the enforcement split exists to prevent.
func TestTheInventoryNarrowsBySourceAsTheSetPathDoes(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "r/one", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "r/two", "warning", "codeql")
	findOn(t, s, "vendor/action-c", "r/three", "warning", "codeql")
	for _, f := range []struct{ node, rule, pack string }{
		{"vendor/action-a", "r/one", upstreamFindings},
		{"vendor/action-b", "r/two", customFindings},
	} {
		run := sourcedRun(t, s, QueryRun{QueryID: f.rule, Pack: f.pack, Repository: "octo-org/app",
			DBCommit: f.node, RecordedBy: RecordedByRun, Rules: []QueryRunRule{{RuleID: f.rule, Pack: f.pack}}})
		if _, err := s.db.Exec(`
			INSERT INTO abom_finding_sighting (finding_row_id, query_run_id)
			SELECT f.row_id, ? FROM abom_finding f
			JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
			JOIN abom_node n ON n.row_id = fn.node_row_id
			WHERE n.node_key = ? AND f.rule_id = ?`, run, f.node, f.rule); err != nil {
			t.Fatal(err)
		}
	}

	for _, sources := range [][]string{{QuerySourceCustom}, {QuerySourceUpstream, QuerySourceCustom}} {
		page, err := s.ActionsInventoryPage(InventoryQuery{Sources: sources})
		if err != nil {
			t.Fatalf("ActionsInventoryPage: %v", err)
		}
		listed := []string{}
		for _, row := range page.Items {
			listed = append(listed, row.NodeKey)
		}
		matched, err := s.NodesMatching(FindingSelector{Sources: sources})
		if err != nil {
			t.Fatal(err)
		}
		chosen := []string{}
		for _, n := range matched.Nodes {
			chosen = append(chosen, n.Key)
		}
		sort.Strings(listed)
		if len(listed) != len(sources) || !reflect.DeepEqual(listed, chosen) {
			t.Errorf("source=%v lists %v and selects %v, want the same %d nodes", sources, listed, chosen, len(sources))
		}
		if page.Unsourced != 1 {
			t.Errorf("source=%v says %d rows carry findings with no recorded source, want vendor/action-c",
				sources, page.Unsourced)
		}
	}
}

// A run over named repositories that also names a source runs where that source found something.
// Taking the names alone, as a run naming only repositories does, would widen it to all of them.
func TestARunNamingRepositoriesStillNarrowsBySource(t *testing.T) {
	s := sourcedStore(t)
	for source, want := range map[string]int{QuerySourceUpstream: 1, QuerySourceCustom: 0} {
		set, err := s.DatabasesMatching(FindingSelector{
			Repositories: []string{"octo-org/app"}, Sources: []string{source}})
		if err != nil {
			t.Fatalf("DatabasesMatching: %v", err)
		}
		accounted := repositoriesOf(set)
		for _, g := range set.Gaps {
			accounted[g.Repository] = true
		}
		if len(accounted) != want {
			t.Errorf("octo-org/app with source=%s resolves to %v, want %d repositories", source, accounted, want)
		}
	}
}

// Asked per finding, a source has to be key lookups — the finding's sightings, the evaluation
// each names for its shape, then the rule it ran — never a scan of every rule every evaluation
// recorded.
func TestASourceIsLookedUpThroughTheFindingsOwnSightings(t *testing.T) {
	s := newTestStore(t)
	clause, args := sourceClause("f", AnalysisModeRegular, []string{QuerySourceUpstream})
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT f.row_id FROM abom_finding f WHERE "+clause, args...)
	if err != nil {
		t.Fatalf("explaining the source filter: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	for _, alias := range []string{"sg", "sq", "qr"} {
		if strings.Contains(joined, "SCAN "+alias) || !strings.Contains(joined, "SEARCH "+alias) {
			t.Errorf("the source filter does not look %s up by key:\n%s", alias, joined)
		}
	}
}

// A rule's count says which sources reported it and how many of its findings none is recorded
// for, so a reader is not left to infer the source from a rule id.
func TestARulesCountSaysWhereItsFindingsCameFrom(t *testing.T) {
	s := sourcedStore(t)
	node, err := s.NodeDetailOf("octo-org/tool/.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("NodeDetailOf: %v", err)
	}
	unscored := SeverityCounts{Unknown: 1}
	want := []RuleCount{
		{RuleID: "acme/checkout-tag-pin", Count: 1, Sources: []string{QuerySourceCustom},
			Severity: unscored},
		{RuleID: "actions/unpinned-tag", Count: 1, Sources: []string{}, Unsourced: 1,
			Severity: unscored},
	}
	if !reflect.DeepEqual(node.Rules, want) {
		t.Errorf("the node's rules read %+v, want %+v", node.Rules, want)
	}

	reportAgainFromCustom(t, s)
	repo, err := s.RepositoryDetailOf("octo-org/app")
	if err != nil {
		t.Fatalf("RepositoryDetailOf: %v", err)
	}
	want = []RuleCount{
		{RuleID: "abom/secrets-inherit", Count: 1, Sources: []string{QuerySourceAbom},
			Severity: unscored},
		{RuleID: "actions/unpinned-tag", Count: 1, Sources: []string{QuerySourceCustom, QuerySourceUpstream},
			Severity: unscored},
	}
	if !reflect.DeepEqual(repo.FindingsByRule, want) {
		t.Errorf("the repository's rules read %+v, want %+v", repo.FindingsByRule, want)
	}
}

// A finding is traced to every evaluation that reported it: what each invoked, the version and
// pack of the rule's own query, the database it read and who ran it.
func TestAFindingIsTracedToEveryEvaluationThatReportedIt(t *testing.T) {
	s := sourcedStore(t)
	again := reportAgainFromCustom(t, s)
	got, err := s.FindingTrace(findingRow(t, s, "octo-org/app", "actions/unpinned-tag"))
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if got.RuleID != "actions/unpinned-tag" || got.Repository != "octo-org/app" || got.StartLine != 100 {
		t.Errorf("traced %+v", got)
	}
	if len(got.Evaluations) != 2 {
		t.Fatalf("traced to %d evaluations, want the lane and the custom run: %+v", len(got.Evaluations), got.Evaluations)
	}
	lane, custom := got.Evaluations[0], got.Evaluations[1]
	if lane.Invoked != "abom-findings.qls" || !reflect.DeepEqual(lane.Packs, []string{upstreamFindings, abomFindings}) ||
		lane.RuleVersion != "u1" || lane.Pack != upstreamFindings || lane.PackVersion != "0.6.36" ||
		lane.PackDigest != "d-up" || lane.Source != QuerySourceUpstream || lane.CLIVersion != "2.27.1" ||
		lane.Mode != AnalysisModeRegular || lane.Commit != "c-app" || lane.RecordedBy != RecordedByCrawl ||
		lane.RanAt == "" || len(lane.Runs) != 0 || lane.Runs == nil {
		t.Errorf("the lane reads %+v", lane)
	}
	if custom.QueryRunID != again || custom.Invoked != "actions/unpinned-tag" || custom.RuleVersion != "x1" ||
		custom.Source != QuerySourceCustom || custom.RecordedBy != RecordedByRun {
		t.Errorf("the custom run reads %+v", custom)
	}
}

// What was not recorded is said rather than guessed: an evaluation that kept no record of its
// rules still says what it invoked, and a finding no evaluation is on record for is traced to none.
func TestATraceLeavesWhatWasNotRecordedEmpty(t *testing.T) {
	s := sourcedStore(t)
	legacy := sourcedRun(t, s, QueryRun{QueryID: "abom-findings.qls", QueryVersion: "old",
		Pack: upstreamFindings + " " + abomFindings, Repository: "octo-org/tool", DBCommit: "c-old"})
	seedFindings(t, s, repoID(t, s, "octo-org/tool"), ".github/workflows/ci.yml",
		Finding{RuleID: "actions/untrusted-checkout", QueryRunID: legacy, Provider: "CodeQL"})

	got, err := s.FindingTrace(findingRow(t, s, "octo-org/tool", "actions/untrusted-checkout"))
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if len(got.Evaluations) != 1 {
		t.Fatalf("traced to %+v", got.Evaluations)
	}
	if e := got.Evaluations[0]; e.Invoked != "abom-findings.qls" || len(e.Packs) != 2 ||
		e.RuleVersion != "" || e.Pack != "" || e.Source != "" || e.RecordedBy != "" {
		t.Errorf("an evaluation that recorded no rules reads %+v", e)
	}

	none, err := s.FindingTrace(findingRow(t, s, "octo-org/tool", "actions/unpinned-tag"))
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if none.Evaluations == nil || len(none.Evaluations) != 0 {
		t.Errorf("a finding no evaluation is on record for is traced to %+v", none.Evaluations)
	}

	if _, err := s.FindingTrace(1 << 40); !errors.Is(err, ErrNoFinding) {
		t.Errorf("tracing a finding the store does not hold returned %v, want ErrNoFinding", err)
	}
}

// A finding a run reported names that run, so the trace reaches the run page and back.
func TestATraceNamesTheRunsAnEvaluationAnswered(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)
	got, err := s.FindingTrace(fired)
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if len(got.Evaluations) != 1 {
		t.Fatalf("traced to %+v", got.Evaluations)
	}
	want := []FindingRun{{RunID: runID, Repository: "octo/app", Status: AnalysisSucceeded}}
	if runs := got.Evaluations[0].Runs; !reflect.DeepEqual(runs, want) {
		t.Errorf("the evaluation names runs %+v, want %+v", runs, want)
	}
}

// A run of several queries is one evaluation per member, so the member's row cannot say which
// version of which pack's query reported a finding. Each result says so itself, or says nothing.
func TestARunsFindingsSayWhichQueryVersionReportedThem(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	held := ScopedDatabase{DatabaseIdentity: DatabaseIdentity{
		Repository: "octo/app", Commit: "c1", Mode: AnalysisModeRegular,
	}, Status: DatabaseRetained, Path: "/db/octo/app"}
	runID, err := s.CreateAnalysisRun(AnalysisRequest{
		RequestedBy: "operator",
		Queries:     []AnalysisQuery{{QueryID: "acme/a", QueryVersion: "v1"}, {QueryID: "acme/b", QueryVersion: "v2"}},
		Databases:   []ScopedDatabase{held},
	})
	if err != nil {
		t.Fatal(err)
	}
	qr := sourcedRun(t, s, QueryRun{QueryID: "acme/a acme/b", QueryVersion: "both", Pack: customFindings,
		Repository: "octo/app", DBCommit: "c1", RecordedBy: RecordedByRun,
		Rules: []QueryRunRule{{RuleID: "acme/a", QueryVersion: "v1", Pack: customFindings}}})
	if _, err := s.AddFindings([]Finding{
		{QueryRunID: qr, RuleID: "acme/a", RepositoryID: repo, FilePath: ".github/workflows/ci.yml", StartLine: 6},
		{QueryRunID: qr, RuleID: "acme/b", RepositoryID: repo, FilePath: ".github/workflows/ci.yml", StartLine: 7},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAnalysisMemberResult(runID, held.DatabaseIdentity, MemberResult{
		Status: AnalysisSucceeded, Counted: true, Results: 2,
		ByQuery: map[string]int{"acme/a": 1, "acme/b": 1}, QueryRunID: qr,
	}); err != nil {
		t.Fatal(err)
	}

	listed, err := s.RunFindings(runID, "octo/app", "", 10)
	if err != nil {
		t.Fatalf("RunFindings: %v", err)
	}
	got := map[string][2]string{}
	for _, f := range listed.Findings {
		got[f.RuleID] = [2]string{f.Source, f.RuleVersion}
	}
	want := map[string][2]string{"acme/a": {QuerySourceCustom, "v1"}, "acme/b": {"", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the member's findings read %v, want %v", got, want)
	}
}

// Who recorded an evaluation is kept from the first recording: the same identity recorded again
// evaluated the same thing, and a later writer must not rewrite who ran it.
func TestAnEvaluationKeepsWhoRecordedItFirst(t *testing.T) {
	s := newTestStore(t)
	q := QueryRun{QueryID: "acme/a", Repository: "octo/app", DBCommit: "c1", RecordedBy: RecordedByCrawl}
	first := sourcedRun(t, s, q)
	q.RecordedBy = RecordedByRun
	if again := sourcedRun(t, s, q); again != first {
		t.Fatalf("one identity recorded twice is rows %d and %d", first, again)
	}
	unsaid := sourcedRun(t, s, QueryRun{QueryID: "acme/b", Repository: "octo/app", DBCommit: "c1"})
	sourcedRun(t, s, QueryRun{QueryID: "acme/b", Repository: "octo/app", DBCommit: "c1", RecordedBy: RecordedByRun})

	for id, want := range map[int64]string{first: RecordedByCrawl, unsaid: RecordedByRun} {
		var got string
		if err := s.db.QueryRow(`SELECT COALESCE(recorded_by, '') FROM abom_query_run WHERE row_id = ?`, id).
			Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("evaluation %d is recorded by %q, want %q", id, got, want)
		}
	}
	if _, err := s.RecordQueryRun(QueryRun{QueryID: "acme/c", Repository: "octo/app",
		AnalysisMode: AnalysisModeRegular, RecordedBy: "cron"}); err == nil {
		t.Error("a recorder ABOM does not know was accepted")
	}
}

type manifestRow struct {
	run                         int64
	rule, version, pack, source string
}

func manifestOf(t *testing.T, s *Store) []manifestRow {
	t.Helper()
	rows, err := s.db.Query(`
		SELECT query_run_id, rule_id, COALESCE(query_version, '<unknown>'), COALESCE(pack, '<unknown>'),
		       COALESCE(source, '<unknown>')
		FROM abom_query_run_rule ORDER BY query_run_id, rule_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []manifestRow{}
	for rows.Next() {
		var r manifestRow
		if err := rows.Scan(&r.run, &r.rule, &r.version, &r.pack, &r.source); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A rule id two packs supplied to one evaluation cannot be attributed to either. It is recorded as
// both packs, which reads as mixed, with no version, rather than as whichever pack came first.
// Recording the evaluation again fills in only what was not known: one run key is one set of pack
// contents, so a version learned later is the same version.
func TestAnEvaluationRecordsEachRuleOnceAndWhereItCameFrom(t *testing.T) {
	s := newTestStore(t)
	q := QueryRun{QueryID: "lane", Pack: customFindings + " " + upstreamFindings, Repository: "octo/app",
		Rules: []QueryRunRule{
			{RuleID: "actions/unpinned-tag", QueryVersion: "u1", Pack: upstreamFindings},
			{RuleID: "actions/unpinned-tag", QueryVersion: "x1", Pack: customFindings},
			{RuleID: "acme/a", Pack: customFindings},
		}}
	run := sourcedRun(t, s, q)
	q.Rules = []QueryRunRule{{RuleID: "acme/a", QueryVersion: "v1", Pack: customFindings}}
	sourcedRun(t, s, q)

	want := []manifestRow{
		{run, "acme/a", "v1", customFindings, QuerySourceCustom},
		{run, "actions/unpinned-tag", "<unknown>", customFindings + " " + upstreamFindings, QuerySourceMixed},
	}
	if got := manifestOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("the evaluation recorded %+v, want %+v", got, want)
	}
}

// Evaluations recorded before rules were kept get a source only where it is certain. One pack
// supplied everything a single-pack evaluation ran, so every rule it reported came from that pack;
// a version is certain only when the evaluation ran that one query, since a lane recorded one hash
// for all of them. An evaluation that drew on several packs stays unknown rather than attributed.
func TestMigrateNamesTheSourceOfEarlierEvaluationsOnlyWhereItIsCertain(t *testing.T) {
	s := subgraphFixture(t)
	single := sourcedRun(t, s, QueryRun{QueryID: "codeql/actions-queries:codeql-suites/actions-code-scanning.qls",
		QueryVersion: "suite-hash", Pack: upstreamFindings, PackVersion: "0.6.35", Repository: "octo-org/app",
		DBCommit: "c1"})
	own := sourcedRun(t, s, QueryRun{QueryID: "acme/checkout-tag-pin", QueryVersion: "c0", Pack: customFindings,
		Repository: "octo-org/tool", DBCommit: "c2"})
	mixed := sourcedRun(t, s, QueryRun{QueryID: "abom-findings.qls", QueryVersion: "m1",
		Pack: upstreamFindings + " " + abomFindings, Repository: "octo-org/tool", DBCommit: "c3"})
	nothing := sourcedRun(t, s, QueryRun{QueryID: "abom-findings.qls", QueryVersion: "n1",
		Repository: "octo-org/app", DBCommit: "c4"})
	seedFindings(t, s, repoID(t, s, "octo-org/app"), ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", QueryRunID: single},
		Finding{RuleID: "abom/secrets-inherit", QueryRunID: nothing},
	)
	seedFindings(t, s, repoID(t, s, "octo-org/tool"), ".github/workflows/ci.yml",
		Finding{RuleID: "acme/checkout-tag-pin", QueryRunID: own},
		Finding{RuleID: "actions/untrusted-checkout", QueryRunID: mixed},
	)
	if got := manifestOf(t, s); len(got) != 0 {
		t.Fatalf("the fixture already records rules: %+v", got)
	}

	want := []manifestRow{
		{single, "actions/unpinned-tag", "<unknown>", upstreamFindings, QuerySourceUpstream},
		{own, "acme/checkout-tag-pin", "c0", customFindings, QuerySourceCustom},
	}
	for pass := 1; pass <= 2; pass++ {
		if err := s.Migrate(); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if got := manifestOf(t, s); !reflect.DeepEqual(got, want) {
			t.Errorf("after migration %d the rules on record are %+v, want %+v", pass, got, want)
		}
	}
}
