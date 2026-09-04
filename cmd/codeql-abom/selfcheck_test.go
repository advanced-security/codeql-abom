package main

import (
	"strings"
	"testing"
)

const twoFindingSARIF = `{
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
              "shortDescription": {"text": "Unpinned tag"},
              "properties": {"kind": "problem", "problem.severity": "warning"}
            },
            {
              "id": "actions/unnecessary-use-of-advanced-config",
              "name": "Unnecessary advanced config",
              "shortDescription": {"text": "Unnecessary advanced config"},
              "properties": {"kind": "problem", "problem.severity": "recommendation"}
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "actions/unpinned-tag",
          "level": "warning",
          "message": {"text": "Reference is not pinned to a full-length commit SHA."},
          "locations": [
            {"physicalLocation": {
              "artifactLocation": {"uri": ".github/workflows/main.yml"},
              "region": {"startLine": 10, "startColumn": 1, "endLine": 10, "endColumn": 20}
            }}
          ]
        },
        {
          "ruleId": "actions/unnecessary-use-of-advanced-config",
          "level": "note",
          "message": {"text": "This step does not need its own advanced configuration."},
          "locations": [
            {"physicalLocation": {
              "artifactLocation": {"uri": ".github/workflows/main.yml"},
              "region": {"startLine": 29, "startColumn": 1, "endLine": 32, "endColumn": 3}
            }}
          ]
        }
      ]
    }
  ]
}`

// The suite reports zero findings far more often than not: most workflows this catches nothing
// in. That must read as a clean run, not an error, so the smoke test exits zero on it.
const noFindingSARIF = `{
  "version": "2.1.0",
  "runs": [
    {"tool": {"driver": {"name": "CodeQL", "rules": []}}, "results": []}
  ]
}`

func TestSummarizeSARIFCountsRulesAndFindingsBySeverity(t *testing.T) {
	summary, err := summarizeSARIF(strings.NewReader(twoFindingSARIF))
	if err != nil {
		t.Fatalf("summarizeSARIF: %v", err)
	}
	if summary.Rules != 2 {
		t.Errorf("Rules = %d, want 2", summary.Rules)
	}
	if summary.Results != 2 {
		t.Errorf("Results = %d, want 2", summary.Results)
	}
	if summary.BySeverity["warning"] != 1 {
		t.Errorf("BySeverity[warning] = %d, want 1", summary.BySeverity["warning"])
	}
	if summary.BySeverity["note"] != 1 {
		t.Errorf("BySeverity[note] = %d, want 1", summary.BySeverity["note"])
	}
}

func TestSummarizeSARIFHandlesNoFindings(t *testing.T) {
	summary, err := summarizeSARIF(strings.NewReader(noFindingSARIF))
	if err != nil {
		t.Fatalf("summarizeSARIF: %v", err)
	}
	if summary.Rules != 0 || summary.Results != 0 {
		t.Errorf("summary = %+v, want all zero", summary)
	}
}

func TestSummarizeSARIFRejectsGarbage(t *testing.T) {
	if _, err := summarizeSARIF(strings.NewReader("not sarif")); err == nil {
		t.Fatal("summarizeSARIF accepted invalid input")
	}
}

func TestWriteSelfcheckSummaryReportsEveryRuleAndSeverity(t *testing.T) {
	var out strings.Builder
	writeSelfcheckSummary(&out, selfcheckSummary{
		Rules: 2, Results: 2, BySeverity: map[string]int{"warning": 1, "note": 1},
	})
	got := out.String()
	for _, want := range []string{"2 rules", "2 findings", "warning=1", "note=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary output = %q, want it to contain %q", got, want)
		}
	}
}
