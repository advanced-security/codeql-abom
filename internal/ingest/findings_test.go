package ingest

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

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
