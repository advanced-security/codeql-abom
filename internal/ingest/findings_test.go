package ingest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestQueryHelpAndResultEvidenceAreRecordedWithTheirEvaluation(t *testing.T) {
	data, err := os.ReadFile("../expand/testdata/findings.sarif")
	if err != nil {
		t.Fatal(err)
	}
	database := newTestStore(t)
	runID, err := database.RecordQueryRun(store.QueryRun{
		QueryID: "actions-suite", Repository: "octo/app", DBCommit: "recorded-revision",
		AnalysisMode: store.AnalysisModeRegular,
	})
	if err != nil {
		t.Fatal(err)
	}
	ingester := New(database, store.NewWriter(database, 0))
	count, err := ingester.FindingsFromSARIF(strings.NewReader(string(data)), Options{Repository: "octo/app", QueryRunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := database.DB().Query(`SELECT rule.documentation_json, sight.evidence_json
		FROM abom_finding_sighting sight
		JOIN abom_finding finding ON finding.row_id = sight.finding_row_id
		JOIN abom_query_run_rule rule ON rule.query_run_id = sight.query_run_id AND rule.rule_id = finding.rule_id
		WHERE sight.query_run_id = ?`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var documentation, evidence string
		if err := rows.Scan(&documentation, &evidence); err != nil {
			t.Fatal(err)
		}
		var help struct {
			Title        string
			HelpMarkdown string
		}
		var result struct {
			RuleID    string
			Locations []json.RawMessage
		}
		if err := json.Unmarshal([]byte(documentation), &help); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(evidence), &result); err != nil {
			t.Fatal(err)
		}
		if help.Title == "" || help.HelpMarkdown == "" || result.RuleID == "" || len(result.Locations) == 0 {
			t.Fatalf("incomplete retained evidence: %+v, %+v", help, result)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != count || seen == 0 {
		t.Fatalf("retained %d evaluation records for %d findings", seen, count)
	}
}

const suiteSarif = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "CodeQL",
          "rules": [
            {
              "id": "actions/unpinned-tag",
              "name": "Unpinned tag",
              "shortDescription": {"text": "Unpinned tag for a non-immutable Action"},
              "properties": {
                "kind": "problem",
                "problem.severity": "warning",
                "security-severity": "5.0",
                "tags": ["security", "actions", "external/cwe/cwe-829"]
              }
            },
            {
              "id": "actions/vulnerable-action",
              "name": "Use of a known vulnerable action",
              "properties": {
                "kind": "problem",
                "problem.severity": "error",
                "security-severity": "7.5"
              }
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "actions/unpinned-tag",
          "level": "warning",
          "message": {"text": "Unpinned 3rd party Action"},
          "locations": [{"physicalLocation": {
            "artifactLocation": {"uri": ".github/workflows/main.yml"},
            "region": {"startLine": 18, "startColumn": 15, "endLine": 18, "endColumn": 35}}}],
          "partialFingerprints": {"primaryLocationLineHash": "hash-a"}
        },
        {
          "ruleId": "actions/vulnerable-action",
          "level": "error",
          "message": {"text": "known vulnerable version"},
          "locations": [{"physicalLocation": {
            "artifactLocation": {"uri": ".github/workflows/main.yml"},
            "region": {"startLine": 22, "startColumn": 15}}}]
        }
      ]
    }
  ]
}`

func TestFindingsFromSARIFStoresRuleMetadata(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	n, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"})
	if err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	if n != 2 {
		t.Fatalf("stored %d findings, want 2", n)
	}

	var severity string
	var securitySeverity float64
	if err := s.DB().QueryRow(`
		SELECT severity, security_severity FROM abom_finding
		WHERE rule_id = 'actions/unpinned-tag'`).Scan(&severity, &securitySeverity); err != nil {
		t.Fatalf("reading finding: %v", err)
	}
	if severity != "warning" {
		t.Errorf("severity = %q, want warning", severity)
	}
	// security-severity comes from the rule, not the result, so it must be joined by rule id.
	if securitySeverity != 5.0 {
		t.Errorf("security_severity = %v, want 5.0", securitySeverity)
	}
}

// A rule that declares no score stores none. Zero is a score code scanning's scale does not have,
// and written for absence it is a convention every reader must remember instead of a fact.
func TestARuleDeclaringNoScoreStoresNoneRatherThanZero(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))
	if _, err := ing.FindingsFromSARIF(strings.NewReader(otherToolSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	var unscored, zero int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FILTER (WHERE security_severity IS NULL),
		       COUNT(*) FILTER (WHERE security_severity = 0)
		FROM abom_finding`).Scan(&unscored, &zero); err != nil {
		t.Fatalf("reading findings: %v", err)
	}
	if unscored != 1 || zero != 0 {
		t.Errorf("stored %d unscored and %d scored zero, want 1 and 0", unscored, zero)
	}
}

// Same shape, different producer. SARIF and code scanning already keep tool names unique within a
// repository, so the name in the document is the identity and needs no massaging here.
const otherToolSarif = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "Some Other Analyzer", "rules": [
        {"id": "other/rule", "properties": {"problem.severity": "warning"}}
      ]}},
      "results": [
        {
          "ruleId": "other/rule",
          "level": "warning",
          "message": {"text": "something"},
          "locations": [{"physicalLocation": {
            "artifactLocation": {"uri": ".github/workflows/main.yml"},
            "region": {"startLine": 4, "startColumn": 1}}}]
        }
      ]
    }
  ]
}`

const namelessToolSarif = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"rules": [
        {"id": "other/rule", "properties": {"problem.severity": "warning"}}
      ]}},
      "results": [
        {
          "ruleId": "other/rule",
          "level": "warning",
          "message": {"text": "something"},
          "locations": [{"physicalLocation": {
            "artifactLocation": {"uri": ".github/workflows/main.yml"},
            "region": {"startLine": 4, "startColumn": 1}}}]
        }
      ]
    }
  ]
}`

// SARIF is the findings surface, and every SARIF document names the tool that wrote it. Reading
// that is the difference between recording a producer and asserting one: a constant at the insert
// says "CodeQL" no matter what the file actually came from.
func TestFindingsFromSARIFAreAttributedToCodeQL(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	var unattributed int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_finding WHERE provider IS NOT ?`,
		store.ProviderCodeQL).Scan(&unattributed); err != nil {
		t.Fatalf("reading providers: %v", err)
	}
	if unattributed != 0 {
		t.Errorf("%d findings are not attributed to %q", unattributed, store.ProviderCodeQL)
	}
}

// The proof that the name is read rather than asserted: the same ingest, a document written by
// something else, and the row says so.
func TestTheProviderIsWhicheverToolWroteTheDocument(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(otherToolSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	var provider string
	if err := s.DB().QueryRow(
		`SELECT COALESCE(provider, '') FROM abom_finding`).Scan(&provider); err != nil {
		t.Fatalf("reading the provider: %v", err)
	}
	if provider != "Some Other Analyzer" {
		t.Errorf("provider = %q, want the tool the document names", provider)
	}
}

// A document that names no tool leaves the producer unknown. SARIF requires the field, so this is
// a malformed file rather than a foreseeable one — and guessing CodeQL on its behalf is exactly
// the fabrication the column exists to prevent.
func TestADocumentThatNamesNoToolRecordsNoProvider(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(namelessToolSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	var provider any
	if err := s.DB().QueryRow(`SELECT provider FROM abom_finding`).Scan(&provider); err != nil {
		t.Fatalf("reading the provider: %v", err)
	}
	if provider != nil {
		t.Errorf("provider = %v, want NULL: nothing said who wrote it", provider)
	}
}

func TestFindingsFromSARIFRecordsLocationAndFingerprint(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	var path string
	var line, col int
	var fingerprint string
	if err := s.DB().QueryRow(`
		SELECT file_path, start_line, start_column, fingerprint FROM abom_finding
		WHERE rule_id = 'actions/unpinned-tag'`).
		Scan(&path, &line, &col, &fingerprint); err != nil {
		t.Fatalf("reading finding: %v", err)
	}
	if path != ".github/workflows/main.yml" || line != 18 || col != 15 {
		t.Errorf("location = %s:%d:%d", path, line, col)
	}
	if fingerprint != "hash-a" {
		t.Errorf("fingerprint = %q, want hash-a", fingerprint)
	}
}

// Re-analyzing an unchanged repository must not multiply findings.
func TestFindingsFromSARIFIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	for i := 0; i < 2; i++ {
		if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
			Options{Repository: "octo-org/app"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	var count int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&count)
	if count != 2 {
		t.Errorf("got %d findings after two runs, want 2", count)
	}
}

// Findings must attach to the graph, or traversal cannot answer security questions.
// The production path end to end, minus CodeQL itself: SARIF in, a sighting out. Recording which
// run reported a finding has to happen here, because a finding row spans commits and can name
// only one run — nothing downstream can reconstruct it afterwards.
func TestIngestingSARIFUnderARunRecordsASighting(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	runID, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", QueryVersion: "1.0.0",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35",
		Repository: "octo-org/app", DBCommit: "cafe1234",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	stored, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app", QueryRunID: runID})
	if err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	var sightings int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_finding_sighting WHERE query_run_id = ?`,
		runID).Scan(&sightings); err != nil {
		t.Fatalf("counting sightings: %v", err)
	}
	if sightings != stored {
		t.Errorf("%d sightings for %d findings; every finding the run reported needs one",
			sightings, stored)
	}
}

func TestFindingsLinkToGraphNodes(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestFixture(t, s)

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	// The fixture's setup-node step is at line 18, matching the unpinned-tag finding.
	var nodeKey string
	if err := s.DB().QueryRow(`
		SELECT n.node_key
		FROM abom_finding f
		JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		JOIN abom_node n ON n.row_id = fn.node_row_id
		WHERE f.rule_id = 'actions/unpinned-tag'`).Scan(&nodeKey); err != nil {
		t.Fatalf("reading linked node: %v", err)
	}
	if !strings.HasPrefix(nodeKey, "step:") {
		t.Errorf("finding linked to %q, want a step node", nodeKey)
	}
}

func TestFindingsFromSARIFRequiresRepository(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif), Options{}); err == nil {
		t.Error("expected an error when Repository is empty")
	}
}

func TestFindingsFromSARIFRejectsEmptyDocument(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	_, err := ing.FindingsFromSARIF(strings.NewReader(`{"version":"2.1.0"}`),
		Options{Repository: "octo-org/app"})
	if err == nil {
		t.Error("expected an error for SARIF with no runs")
	}
}

// A SARIF file with rules but no results is normal for a clean repository.
func TestFindingsFromSARIFHandlesNoResults(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	const clean = `{"runs":[{"tool":{"driver":{"rules":[{"id":"actions/unpinned-tag"}]}},
	                "results":[]}]}`
	n, err := ing.FindingsFromSARIF(strings.NewReader(clean), Options{Repository: "octo-org/app"})
	if err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	if n != 0 {
		t.Errorf("stored %d findings, want 0", n)
	}
}

// A run that names no rule at all searched for nothing, which is not the same as searching and
// finding nothing — and it is indistinguishable from a clean repository once stored.
//
// Not hypothetical: `database interpret-results` over a results tree it does not recognize exits
// 0, says "Exported results to SARIF", and writes exactly this. Every repository would have come
// back clean and the crawl would have looked healthy.
func TestFindingsFromSARIFRejectsARunThatSearchedForNothing(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	const empty = `{"runs":[{"tool":{"driver":{"name":"CodeQL","rules":[]}},"results":[]}]}`
	_, err := ing.FindingsFromSARIF(strings.NewReader(empty), Options{Repository: "octo-org/app"})
	if err == nil {
		t.Fatal("a run that names no rules was accepted as a clean repository")
	}
	if !strings.Contains(err.Error(), "octo-org/app") {
		t.Errorf("the error does not name the repository: %v", err)
	}
}
