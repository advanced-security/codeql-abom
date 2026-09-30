package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/sarifin"
)

func TestFindingReviewNeverBorrowsEvidenceFromAnotherAnalysisMode(t *testing.T) {
	database := newTestStore(t)
	repositoryID, err := database.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open("../expand/testdata/findings.sarif")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rules := map[string]sarifin.Rule{}
	var result sarifin.Result
	if err := sarifin.Stream(file, sarifin.Handler{
		OnRule:   func(rule sarifin.Rule) error { rules[rule.ID] = rule; return nil },
		OnResult: func(found sarifin.Result) error { result = found; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	documentation, err := json.Marshal(rules[result.RuleID])
	if err != nil {
		t.Fatal(err)
	}
	var extendedRun int64
	for _, mode := range []string{AnalysisModeRegular, AnalysisModeExtended} {
		runID, err := database.RecordQueryRun(QueryRun{QueryID: "fixture", Repository: "octo/app", AnalysisMode: mode, DBCommit: mode})
		if err != nil {
			t.Fatal(err)
		}
		finding := Finding{RepositoryID: repositoryID, QueryRunID: runID, RuleID: result.RuleID,
			FilePath: result.FilePath, StartLine: result.Region.StartLine, Message: result.Message}
		if mode == AnalysisModeExtended {
			extendedRun = runID
			finding.RuleDocumentation, finding.Evidence = documentation, result.Evidence
		}
		if _, err := database.AddFindings([]Finding{finding}); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{AnalysisModeRegular, AnalysisModeExtended} {
		query := FindingReviewQuery{InventoryQuery: InventoryQuery{Mode: mode}}
		page, err := database.InventoryFindingsPage(query)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s: %+v, %v", mode, page, err)
		}
		query.FindingID = page.Items[0].ID
		details, err := database.FindingReviewDetails(query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if (len(details.Documentation) > 0) != (mode == AnalysisModeExtended) || len(details.Evaluations) != 1 {
			t.Fatalf("%s borrowed evidence: %+v", mode, details)
		}
		if mode == AnalysisModeRegular {
			if _, err := database.FindingReviewDetails(query, extendedRun); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("accepted an evaluation outside the selected mode: %v", err)
			}
		}
	}
}

func TestAnAssetReviewMatchesItsInventoryCountAndDoesNotPoolItsNeighbors(t *testing.T) {
	database := advisoryStore(t)
	inventoryCorpus(t, database, 2)
	workflowWithJobs(t, database, "octo-org/app", ".github/workflows/release.yml", "build", 2)
	keys := []string{"vendor/action-a", "vendor/action-b", "octo-org/app/.github/workflows/release.yml",
		"job:octo-org/app:.github/workflows/release.yml:build"}
	for _, key := range keys {
		findOn(t, database, key, "actions/example", "warning", "CodeQL")
	}
	for _, key := range keys {
		page, err := database.InventoryFindingsPage(FindingReviewQuery{AssetKey: key, AssetKind: "node"})
		if err != nil || page.Total != 1 || len(page.Items) != 1 {
			t.Fatalf("review for %s pooled neighboring findings: %+v, %v", key, page, err)
		}
	}
	repository, err := database.InventoryFindingsPage(FindingReviewQuery{AssetKey: "octo-org/app", AssetKind: "repository"})
	if err != nil || repository.Total != len(keys) {
		t.Fatalf("repository review lost its findings: %+v, %v", repository, err)
	}
	jobs, err := database.InventoryFindingsPage(FindingReviewQuery{AssetKey: "octo-org/app", AssetKind: "repository",
		InventoryQuery: InventoryQuery{Kinds: []string{"job"}}})
	if err != nil || jobs.Total != 1 {
		t.Fatalf("repository review ignored the node predicate: %+v, %v", jobs, err)
	}
	missing, err := database.InventoryFindingsPage(FindingReviewQuery{AssetKey: "missing/node", AssetKind: "node"})
	if err != nil || missing.Total != 0 {
		t.Fatalf("unknown asset broadened the answer: %+v, %v", missing, err)
	}
}

// findOn records a finding against a node, as SARIF ingestion would. An empty provider is written
// as NULL, because that is what an unattributed finding looks like in the corpus.
func findOn(t *testing.T, s *Store, nodeKey, rule, severity, provider string) {
	t.Helper()
	var nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = ?`, nodeKey).Scan(&nodeID); err != nil {
		t.Fatalf("resolving %s: %v", nodeKey, err)
	}
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	res, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, severity, provider, fingerprint)
		VALUES (?, ?, ?, ?, ?)`,
		rule, repoID, severity, nullable(provider), nodeKey+rule+severity)
	if err != nil {
		t.Fatalf("recording a finding: %v", err)
	}
	findingID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO abom_finding_node (finding_row_id, node_row_id) VALUES (?, ?)`,
		findingID, nodeID); err != nil {
		t.Fatalf("attaching the finding to %s: %v", nodeKey, err)
	}
}

// findIn records a finding in a named repository and attaches it to a node, so the repository the
// finding sits in is distinguishable from the node it is about.
func findIn(t *testing.T, s *Store, repo, nodeKey, rule, severity string) {
	t.Helper()
	var nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = ?`, nodeKey).Scan(&nodeID); err != nil {
		t.Fatalf("resolving %s: %v", nodeKey, err)
	}
	repoID, err := s.UpsertRepository(repo, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	res, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, severity, provider, fingerprint)
		VALUES (?, ?, ?, 'CodeQL', ?)`, rule, repoID, severity, repo+nodeKey+rule)
	if err != nil {
		t.Fatalf("recording a finding: %v", err)
	}
	findingID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO abom_finding_node (finding_row_id, node_row_id) VALUES (?, ?)`,
		findingID, nodeID); err != nil {
		t.Fatalf("attaching the finding to %s: %v", nodeKey, err)
	}
}

// stepNode adds a `step` node, which is the kind the pinning rule actually lands on and which the
// listing's default five deliberately leave out.
func stepNode(t *testing.T, s *Store, repo, key string) {
	t.Helper()
	repoID, err := s.UpsertRepository(repo, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node (node_key, node_kind, repository_row_id, is_external)
		VALUES (?, 'step', ?, 0)`, key, repoID); err != nil {
		t.Fatalf("adding a step node: %v", err)
	}
}

// The node listing refused every findings term, so a reader arriving from "what carries a critical
// finding" was shown a refusal rather than the rows — on a tool whose whole subject is severity
// against reach. These pin the four it can answer, with the meaning the set path already gives
// them: the same predicate has to select the same findings wherever it is asked.
func TestInventoryRulesKeepsOnlyNodesCarryingThatRule(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "actions/secrets-inherit", "error", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 carrying that rule", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-a" {
		t.Errorf("listed %q, want the node the finding is on", page.Items[0].NodeKey)
	}
}

// A trailing `*` is a prefix, because that is what the set path means by one. Two surfaces reading
// one predicate differently is the divergence the enforcement split exists to prevent.
func TestInventoryRulesTreatsATrailingStarAsAPrefix(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "other/rule", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{Rules: []string{"actions/*"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 under that prefix", page.Total)
	}
}

func TestInventorySeverityKeepsOnlyNodesCarryingThatSeverity(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "r/one", "error", "codeql")
	findOn(t, s, "vendor/action-b", "r/two", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{Severity: "error"})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 carrying an error", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-a" {
		t.Errorf("listed %q, want the node with the error", page.Items[0].NodeKey)
	}
}

// A NULL provider is unknown, and unknown is not a match. Treating it as one would attribute a
// finding to a producer that never claimed it.
func TestInventoryProviderDoesNotMatchAnUnattributedFinding(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "r/one", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "r/two", "warning", "")

	page, err := s.ActionsInventoryPage(InventoryQuery{Provider: "codeql"})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want only the attributed one", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-a" {
		t.Errorf("listed %q, want the node whose finding names a producer", page.Items[0].NodeKey)
	}
}

// notRules removes a node that carries the named rule *at all*, rather than removing that rule from
// a node that also carries others. "Show me what is wrong apart from pinning" is the question, and
// a node still carrying an excluded finding is not an answer to it.
func TestInventoryNotRulesRemovesTheNodeRatherThanTheFinding(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")
	findOn(t, s, "vendor/action-a", "other/rule", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "other/rule", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Flags: []string{"findings"}, NotRules: []string{"actions/unpinned-tag"},
	})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want only the node carrying none of it", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-b" {
		t.Errorf("listed %q, want the node free of the excluded rule", page.Items[0].NodeKey)
	}
}

// Two findings terms name one finding, as they do in the set path. Read as two independent
// questions they would match a node carrying an unrelated error and an unrelated pinning warning,
// which is a node nothing is actually wrong with in the way the reader asked about.
func TestInventoryFindingTermsDescribeOneFinding(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")
	findOn(t, s, "vendor/action-a", "other/rule", "error", "codeql")
	findOn(t, s, "vendor/action-b", "actions/unpinned-tag", "error", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Rules: []string{"actions/unpinned-tag"}, Severity: "error",
	})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 whose *same* finding is both", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-b" {
		t.Errorf("listed %q, want the node whose one finding matches both terms",
			page.Items[0].NodeKey)
	}
}

// A findings term can match kinds outside the inventory view; the page must say where.
func TestInventoryReportsWhereAFindingsTermMatchedOutsideTheKindsShown(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	stepNode(t, s, "octo-org/app", "octo-org/app/.github/workflows/ci.yml#step1")
	findOn(t, s, "octo-org/app/.github/workflows/ci.yml#step1",
		"actions/unpinned-tag", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("matched %d rows among the default kinds, want 0", page.Total)
	}
	if len(page.Elsewhere) != 1 {
		t.Fatalf("named %d kinds outside the view, want the 1 it matched: %+v",
			len(page.Elsewhere), page.Elsewhere)
	}
	if page.Elsewhere[0].Kind != "step" || page.Elsewhere[0].Count != 1 {
		t.Errorf("reported %+v, want 1 step", page.Elsewhere[0])
	}
}

// Silent when there is nothing to disclose. A page that always carried the sentence would train a
// reader to stop reading it, which costs exactly the case it exists for.
func TestInventorySaysNothingAboutElsewhereWhenTheMatchIsWhollyOnScreen(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want 1", page.Total)
	}
	if len(page.Elsewhere) != 0 {
		t.Errorf("reported %+v, want nothing: the whole match is on screen", page.Elsewhere)
	}
}

// Asking for a kind by name means the reader is already looking there, so it is not "elsewhere".
func TestInventoryNamesNoKindTheReaderAskedFor(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	stepNode(t, s, "octo-org/app", "octo-org/app/.github/workflows/ci.yml#step1")
	findOn(t, s, "octo-org/app/.github/workflows/ci.yml#step1",
		"actions/unpinned-tag", "warning", "codeql")

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Kinds: []string{"step"}, Rules: []string{"actions/unpinned-tag"},
	})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 step", page.Total)
	}
	if len(page.Elsewhere) != 0 {
		t.Errorf("reported %+v, want nothing: the reader named that kind", page.Elsewhere)
	}
}

// `repositories` takes the set path's meaning — findings *in* these repositories — because one word
// meaning "used by" here and "findings in" there is the divergence the enforcement split exists to
// prevent. "Which dependencies does this repository use" is `scope`, and the graph.
func TestInventoryRepositoriesMeansFindingsInThoseRepositories(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	seedDependency(t, s, "other-org/app", "vendor/action-b", "v1", 1)
	findIn(t, s, "octo-org/app", "vendor/action-a", "r/one", "warning")
	findIn(t, s, "other-org/app", "vendor/action-b", "r/one", "warning")

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Repositories: []string{"octo-org/app"},
	})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d rows, want the 1 whose finding is in that repository", page.Total)
	}
	if page.Items[0].NodeKey != "vendor/action-a" {
		t.Errorf("listed %q, want the node found in the named repository", page.Items[0].NodeKey)
	}
}
