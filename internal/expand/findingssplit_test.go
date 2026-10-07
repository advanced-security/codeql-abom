package expand

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/sarifin"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ownLaneFindings is `database analyze` output for ABOM's lane under the fixture's CodeQL (regen-closure-fixture.sh).
const ownLaneFindings = "../../testdata/closure/regular-octo-app-findings.sarif"

// laneAnalyzer stands in for `database analyze`, recording what it was asked to evaluate.
type laneAnalyzer struct {
	mu      sync.Mutex
	targets [][]string
}

func (a *laneAnalyzer) AnalyzeQueries(_ context.Context, _ string, targets []string, sarifPath string) error {
	a.mu.Lock()
	a.targets = append(a.targets, slices.Clone(targets))
	a.mu.Unlock()
	return copyFixture(ownLaneFindings, sarifPath)
}

func (a *laneAnalyzer) asked() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.targets)
}

// The lane: code scanning's security-extended run listed the first two rules, not the third; the fourth is ABOM's.
const (
	ruleUnpinned    = "actions/unpinned-tag"
	ruleInjection   = "actions/code-injection/critical"
	ruleUnversioned = "actions/unversioned-immutable-action"
	ruleLockMissing = "abom/actions/actions-lock-missing-for-workflow"
)

var laneRules = []string{ruleUnpinned, ruleInjection, ruleUnversioned, ruleLockMissing}

// lanePacks writes the lane's queries into packs under root, the upstream ones at version, by rule.
func lanePacks(t *testing.T, root, version string) map[string]string {
	t.Helper()
	return lanePacksFrom(t, root, "codeql/actions-queries", version)
}

// lanePacksFrom is lanePacks with the upstream rules supplied by pack, as a pack importing them would.
func lanePacksFrom(t *testing.T, root, pack, version string) map[string]string {
	t.Helper()
	body := "from int i select i\n"
	return map[string]string{
		ruleUnpinned:    queryInPack(t, root, pack, version, "UnpinnedActionsTag.ql", body),
		ruleInjection:   queryInPack(t, root, pack, version, "CodeInjectionCritical.ql", body),
		ruleUnversioned: queryInPack(t, root, pack, version, "UnversionedImmutableAction.ql", body),
		ruleLockMissing: queryInPack(t, root, "advanced-security/abom-actions-findings-src", "2.27.1",
			"ActionsLockMissingForWorkflow.ql", body),
	}
}

// laneResolver resolves the findings suite to the lane in packs, under the fixture's CodeQL.
func laneResolver(r suiteResolver, packs map[string]string) suiteResolver {
	for _, rule := range laneRules {
		if packs[rule] == "" {
			continue
		}
		r.suites["findings.qls"] = append(r.suites["findings.qls"], packs[rule])
		r.meta[packs[rule]] = runner.Metadata{ID: rule, Kind: "problem"}
	}
	return r
}

// splitHarness crawls the fixture's repository as an owned one, bound as the defaults bind: graph, then findings.
func splitHarness(t *testing.T, packs map[string]string) (*harness, *fakeCodeScanning, *laneAnalyzer) {
	t.Helper()
	return splitHarnessAt(t, packs, codeScanningCLI)
}

// splitHarnessAt is splitHarness with ABOM running cli.
func splitHarnessAt(t *testing.T, packs map[string]string, cli string) (*harness, *fakeCodeScanning, *laneAnalyzer) {
	t.Helper()
	h, cs := reuseHarness(t, cli)
	h.expander.Resolver = laneResolver(h.expander.Resolver.(suiteResolver), packs)
	bindSuites(t, h, "graph.qls", "findings.qls")
	a := &laneAnalyzer{}
	h.expander.Analyzer = a
	return h, cs, a
}

func findingsByRule(t *testing.T, h *harness) map[string]int {
	t.Helper()
	rows, err := h.store.DB().Query(`SELECT rule_id, COUNT(*) FROM abom_finding GROUP BY rule_id`)
	if err != nil {
		t.Fatalf("counting findings: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var rule string
		var n int
		if err := rows.Scan(&rule, &n); err != nil {
			t.Fatal(err)
		}
		out[rule] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// laneRun is the findings run recorded for the fixture's commit.
func laneRun(t *testing.T, h *harness) (id int64, key string) {
	t.Helper()
	if err := h.store.DB().QueryRow(`SELECT row_id, run_key FROM abom_query_run
		WHERE query_id = 'findings.qls' AND db_commit = ?`, codeScanningCommit).Scan(&id, &key); err != nil {
		t.Fatalf("reading the findings run: %v", err)
	}
	return id, key
}

// ruleRecord is what the findings run records about one rule: what produced it, and from where.
type ruleRecord struct {
	takenFrom, cli, pack, packVersion, queryVersion sql.NullString
}

func recordOf(t *testing.T, h *harness, rule string) ruleRecord {
	t.Helper()
	run, _ := laneRun(t, h)
	var r ruleRecord
	if err := h.store.DB().QueryRow(`SELECT taken_from, cli_version, pack, pack_version, query_version
		FROM abom_query_run_rule WHERE query_run_id = ? AND rule_id = ?`, run, rule).
		Scan(&r.takenFrom, &r.cli, &r.pack, &r.packVersion, &r.queryVersion); err != nil {
		t.Fatalf("reading what the run records of %s: %v", rule, err)
	}
	return r
}

// advancedSetupKey is the analysis key of a repository's own CodeQL workflow, which may narrow the files read.
const advancedSetupKey = ".github/workflows/codeql.yml:analyze"

// codeScanningRelease is the release the fixture's analysis names for codeql/actions-queries.
const codeScanningRelease = "0.6.36+6e9f9e38390175c41b99070a423c875f450759ca"

// tookFromTheFixture asserts each taken rule is recorded as the fixture's analysis produced it, and the rest as ABOM's.
func tookFromTheFixture(t *testing.T, h *harness, taken ...string) {
	t.Helper()
	for _, rule := range laneRules {
		r := recordOf(t, h, rule)
		if !slices.Contains(taken, rule) {
			if r.takenFrom.Valid || r.cli.Valid || !r.queryVersion.Valid {
				t.Errorf("%s, which ABOM evaluated, is recorded as %+v", rule, r)
			}
			continue
		}
		if r.takenFrom.String != "github-code-scanning:1894557161" || r.cli.String != codeScanningCLI ||
			r.pack.String != "codeql/actions-queries" || r.packVersion.String != codeScanningRelease || r.queryVersion.Valid {
			t.Errorf("%s, taken from code scanning, is recorded as %+v", rule, r)
		}
	}
}

// Code scanning's analysis stands in for the rules it ran, those that found nothing included; ABOM runs the rest.
func TestCodeScanningsAnalysisStandsInForTheRulesItRan(t *testing.T) {
	packs := lanePacks(t, t.TempDir(), "0.6.36")
	h, _, a := splitHarness(t, packs)

	if stats := runBound(t, h); stats.Failed != 0 {
		t.Fatalf("%d repositories failed", stats.Failed)
	}

	want := []string{packs[ruleUnversioned], packs[ruleLockMissing]}
	if got := a.asked(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("analyze ran %v, want once for only the rules code scanning did not run, %v", got, want)
	}
	// Each rule's results come from the one evaluation that ran it, and none from a rule outside the lane.
	if got := findingsByRule(t, h); !maps.Equal(got, map[string]int{ruleUnpinned: 1, ruleLockMissing: 1}) {
		t.Errorf("findings by rule = %v, want code scanning's one for %s and ABOM's one for %s",
			got, ruleUnpinned, ruleLockMissing)
	}
	var path string
	var line int
	if err := h.store.DB().QueryRow(`SELECT file_path, start_line FROM abom_finding WHERE rule_id = ?`,
		ruleUnpinned).Scan(&path, &line); err != nil || path != ".github/workflows/ci.yml" || line != 11 {
		t.Errorf("the %s finding is at %s:%d (%v), want code scanning's at .github/workflows/ci.yml:11",
			ruleUnpinned, path, line, err)
	}
	tookFromTheFixture(t, h, ruleUnpinned, ruleInjection)
	run, _ := laneRun(t, h)
	// A taken rule that found nothing is a rule that was evaluated, so the run lists it.
	ran := map[string]bool{}
	rows, err := h.store.DB().Query(`SELECT rule_id FROM abom_query_run_rule WHERE query_run_id = ?`, run)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var rule string
		if err := rows.Scan(&rule); err != nil {
			t.Fatal(err)
		}
		ran[rule] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, rule := range laneRules {
		if !ran[rule] {
			t.Errorf("the findings run does not list %s, which it evaluated", rule)
		}
	}
	if got := h.expander.ReuseCounts(); got.Databases != 1 || got.Analyses != 1 || len(got.Refused) != 0 {
		t.Errorf("reuse = %+v, want one database and one analysis taken", got)
	}
}

// The findings run is the lane's unit of work at the commit, so no skip can tell the two apart; its rules say who answered.
func TestTheFindingsRunKeepsTheIdentityABOMsOwnEvaluationWouldHaveHad(t *testing.T) {
	packs := lanePacks(t, t.TempDir(), "0.6.36")
	took, _, _ := splitHarness(t, packs)
	runBound(t, took)
	own, cs, _ := splitHarness(t, packs)
	cs.analyses[0].AnalysisKey = advancedSetupKey
	runBound(t, own)

	_, tookKey := laneRun(t, took)
	_, ownKey := laneRun(t, own)
	if tookKey != ownKey {
		t.Errorf("the run that took rules is %s and ABOM's own is %s, want one identity", tookKey, ownKey)
	}
	tookFromTheFixture(t, took, ruleUnpinned, ruleInjection)
	tookFromTheFixture(t, own)
}

// A rule id names one query whatever pack, suite or release brought it, so code scanning's analysis
// of the commit answers for it under ABOM's lane: code scanning is unlikely ever to run ABOM's packs.
func TestARuleIsAnsweredByItsIdWhateverPackOrReleaseRanIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		packs func(t *testing.T) map[string]string
	}{
		{name: "ABOM pins another release", packs: func(t *testing.T) map[string]string {
			return lanePacks(t, t.TempDir(), "0.6.40")
		}},
		{name: "ABOM's pack imports the rules", packs: func(t *testing.T) map[string]string {
			return lanePacksFrom(t, t.TempDir(), "octo-org/actions-security", "1.2.0")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			packs := c.packs(t)
			h, _, a := splitHarness(t, packs)

			if stats := runBound(t, h); stats.Failed != 0 {
				t.Fatalf("%d repositories failed", stats.Failed)
			}

			want := []string{packs[ruleUnversioned], packs[ruleLockMissing]}
			if got := a.asked(); len(got) != 1 || !slices.Equal(got[0], want) {
				t.Errorf("analyze ran %v, want only the rules code scanning did not run, %v", got, want)
			}
			if got := findingsByRule(t, h); !maps.Equal(got, map[string]int{ruleUnpinned: 1, ruleLockMissing: 1}) {
				t.Errorf("findings by rule = %v, want code scanning's one and ABOM's one", got)
			}
			tookFromTheFixture(t, h, ruleUnpinned, ruleInjection)
		})
	}
}

// The analysis answers for its commit, not for a database: where ABOM runs another CodeQL it builds
// its own, and the rules code scanning ran are still its, read and recorded under the CodeQL that ran them.
func TestAnAnalysisAnswersForItsCommitWhateverCodeQLABOMRuns(t *testing.T) {
	packs := lanePacks(t, t.TempDir(), "0.6.36")
	// A lane code scanning ran whole, so ABOM evaluates nothing and every result is code scanning's.
	delete(packs, ruleUnversioned)
	delete(packs, ruleLockMissing)
	h, _, a := splitHarnessAt(t, packs, "2.27.2")

	if stats := runBound(t, h); stats.Failed != 0 {
		t.Fatalf("%d repositories failed", stats.Failed)
	}

	if built := h.databasesBuilt(); built != 1 {
		t.Errorf("built %d databases, want ABOM's own: code scanning's is another CodeQL's", built)
	}
	if got := a.asked(); len(got) != 0 {
		t.Errorf("analyze ran %v, want nothing: code scanning answered every rule", got)
	}
	if got := findingsByRule(t, h); !maps.Equal(got, map[string]int{ruleUnpinned: 1}) {
		t.Errorf("findings by rule = %v, want code scanning's one, read under its own CodeQL", got)
	}
	for _, rule := range []string{ruleUnpinned, ruleInjection} {
		if r := recordOf(t, h, rule); r.takenFrom.String != "github-code-scanning:1894557161" || r.cli.String != codeScanningCLI {
			t.Errorf("%s is recorded as %+v, want code scanning's analysis under CodeQL %s", rule, r, codeScanningCLI)
		}
	}
	if got := h.expander.ReuseCounts(); got.Analyses != 1 || got.Refused["database: another CodeQL"] != 1 {
		t.Errorf("reuse = %+v, want the analysis taken beside a database refused for another CodeQL", got)
	}
}

// Only default setup's analysis says which files it read; whatever cannot be established leaves the whole lane to ABOM, counted.
func TestAnAnalysisThatIsNotTheOneABOMWouldRunLeavesTheWholeLane(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		arrange      func(*fakeCodeScanning)
	}{
		{name: "another setup made it", reason: "another configuration",
			arrange: func(cs *fakeCodeScanning) { cs.analyses[0].AnalysisKey = advancedSetupKey }},
		{name: "no analysis of this commit", reason: "no analysis",
			arrange: func(cs *fakeCodeScanning) { cs.analyses[0].CommitSHA = otherCommit }},
		{name: "no analysis at all", reason: "no analysis",
			arrange: func(cs *fakeCodeScanning) {
				cs.analysesErr = &catalog.NoAnalysisError{Repository: codeScanningRepo, Reason: "no analysis found"}
			}},
		{name: "its document is not readable", reason: "not readable",
			arrange: func(cs *fakeCodeScanning) { cs.sarifErr = &catalog.StatusError{Code: 403} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, cs, a := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
			if c.arrange != nil {
				c.arrange(cs)
			}

			if stats := runBound(t, h); stats.Failed != 0 {
				t.Errorf("%d repositories failed; a refused analysis has to cost the lane and nothing else", stats.Failed)
			}

			if got := a.asked(); len(got) != 1 || !slices.Equal(got[0], []string{"findings.qls"}) {
				t.Errorf("analyze ran %v, want the whole lane once, as its suite", got)
			}
			got := h.expander.ReuseCounts()
			if got.Databases != 1 || got.Analyses != 0 || got.Refused["analysis: "+c.reason] != 1 || len(got.Refused) != 1 {
				t.Errorf("reuse = %+v, want the database taken and only analysis: %s", got, c.reason)
			}
			tookFromTheFixture(t, h)
		})
	}
}

// Analyses need code scanning read, held only where the App is installed, so only owned repositories are asked.
func TestOnlyAnOwnedRepositorysAnalysisIsAskedFor(t *testing.T) {
	h, cs, a := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
	if err := h.store.SetOwnedScope([]store.DeclaredAccount{{
		Owner: "octo-org", Kind: store.AccountOrganization, Enabled: true,
	}}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	runBound(t, h)

	if cs.asks() != 0 {
		t.Errorf("asked code scanning %d times about an analysis of a repository nobody here owns", cs.asks())
	}
	if got := a.asked(); len(got) != 1 || !slices.Equal(got[0], []string{"findings.qls"}) {
		t.Errorf("analyze ran %v, want the whole lane", got)
	}
	if got := h.expander.ReuseCounts(); got.Databases != 1 || len(got.Refused) != 0 {
		t.Errorf("reuse = %+v, want its database taken and nothing counted against an analysis", got)
	}
}

// plannedLane is what the planner makes of the lane in packs under cli.
func plannedLane(t *testing.T, packs map[string]string, cli string) tierPlan {
	t.Helper()
	r := laneResolver(suiteResolver{suites: map[string][]string{}, meta: map[string]runner.Metadata{}, version: cli}, packs)
	planned, err := (&Expander{Resolver: r}).plan(context.Background(), Options{Suite: "findings.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return planned
}

// A document answers the lane rules it lists, whatever CodeQL or release it names; a rule the CLI
// lists under its driver rather than a pack names no pack.
func TestCodeScanningsDocumentIsReadAgainstTheLane(t *testing.T) {
	served := codeScanningDir + "analysis.sarif"
	for _, c := range []struct {
		name, document, cli, pack string
		answered                  map[string]store.QueryRunRule
	}{
		{name: "the same CodeQL and release", document: served, cli: "2.27.1", pack: "0.6.36",
			answered: servedRules(ruleInjection, ruleUnpinned)},
		{name: "another CodeQL", document: served, cli: "2.27.0", pack: "0.6.36",
			answered: servedRules(ruleInjection, ruleUnpinned)},
		{name: "another release", document: served, cli: "2.27.1", pack: "0.6.35",
			answered: servedRules(ruleInjection, ruleUnpinned)},
		{name: "rules the CLI lists under its driver", document: ownLaneFindings, cli: "2.27.1", pack: "0.6.36",
			answered: map[string]store.QueryRunRule{
				ruleUnpinned:    {RuleID: ruleUnpinned, CLIVersion: "2.27.1"},
				ruleInjection:   {RuleID: ruleInjection, CLIVersion: "2.27.1"},
				ruleUnversioned: {RuleID: ruleUnversioned, CLIVersion: "2.27.1"},
				ruleLockMissing: {RuleID: ruleLockMissing, CLIVersion: "2.27.1"},
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			answered, reason := answeredRules(c.document, plannedLane(t, lanePacks(t, t.TempDir(), c.pack), c.cli))
			if reason != "" || !maps.Equal(answered, c.answered) {
				t.Errorf("answered %v (%q), want %v", answered, reason, c.answered)
			}
		})
	}
}

// servedRules is how the fixture's served document records each of rules: under the pack that ran it.
func servedRules(rules ...string) map[string]store.QueryRunRule {
	out := map[string]store.QueryRunRule{}
	for _, rule := range rules {
		out[rule] = store.QueryRunRule{RuleID: rule, Pack: "codeql/actions-queries",
			PackVersion: codeScanningRelease, CLIVersion: codeScanningCLI}
	}
	return out
}

// Code scanning's document keeps only the results of the rules it answers, each tool whole so rule indexes hold.
func TestCodeScanningsDocumentKeepsOnlyTheRulesItAnswers(t *testing.T) {
	kept := filepath.Join(t.TempDir(), "code-scanning.sarif")
	if err := copyFixture(codeScanningDir+"analysis.sarif", kept); err != nil {
		t.Fatal(err)
	}
	if err := keepResults(kept, func(rule string) bool { return rule == ruleInjection }); err != nil {
		t.Fatalf("keepResults: %v", err)
	}
	f, err := os.Open(kept)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	results := 0
	if err := sarifin.Stream(f, sarifin.Handler{OnResult: func(sarifin.Result) error { results++; return nil }}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if results != 0 {
		t.Errorf("kept %d results of a rule it does not answer", results)
	}

	tools := func(path string) []any {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Runs []struct {
				Tool any `json:"tool"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		var out []any
		for _, r := range doc.Runs {
			out = append(out, r.Tool)
		}
		return out
	}
	if !reflect.DeepEqual(tools(kept), tools(codeScanningDir+"analysis.sarif")) {
		t.Error("a run's tool changed, so its results' rule indexes may name other rules")
	}
}

// A rule id names one query whichever packs supply it, so a rule the lane takes from two packs is still answered.
func TestARuleTwoPacksSupplyIsStillAnswered(t *testing.T) {
	root := t.TempDir()
	r := laneResolver(suiteResolver{suites: map[string][]string{}, meta: map[string]runner.Metadata{},
		version: codeScanningCLI}, lanePacks(t, root, "0.6.36"))
	ours := queryInPack(t, root, "advanced-security/abom-actions-findings-src", "2.27.1", "Unpinned.ql",
		"from int i select i\n")
	r.suites["findings.qls"] = append(r.suites["findings.qls"], ours)
	r.meta[ours] = runner.Metadata{ID: ruleUnpinned, Kind: "problem"}
	planned, err := (&Expander{Resolver: r}).plan(context.Background(), Options{Suite: "findings.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	answered, reason := answeredRules(codeScanningDir+"analysis.sarif", planned)
	if reason != "" || !maps.Equal(answered, servedRules(ruleInjection, ruleUnpinned)) {
		t.Errorf("answered %v (%q), want %s and %s", answered, reason, ruleInjection, ruleUnpinned)
	}
}

// An analysis that ran none of the lane's rules stands in for nothing, whatever else agrees.
func TestAnAnalysisThatRanNoneOfTheLanesRulesStandsInForNothing(t *testing.T) {
	packs := lanePacks(t, t.TempDir(), "0.6.36")
	r := suiteResolver{suites: map[string][]string{"findings.qls": {packs[ruleUnversioned], packs[ruleLockMissing]}},
		meta: map[string]runner.Metadata{
			packs[ruleUnversioned]: {ID: ruleUnversioned, Kind: "problem"},
			packs[ruleLockMissing]: {ID: ruleLockMissing, Kind: "problem"},
		}, version: codeScanningCLI}
	planned, err := (&Expander{Resolver: r}).plan(context.Background(), Options{Suite: "findings.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if rules, reason := answeredRules(codeScanningDir+"analysis.sarif", planned); reason != "no rules in common" {
		t.Errorf("took %v (%q), want nothing, refused as no rules in common", rules, reason)
	}
}
