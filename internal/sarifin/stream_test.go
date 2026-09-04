package sarifin

import (
	"errors"
	"strings"
	"testing"
)

const codeFlowSarif = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "CodeQL",
          "rules": [
            {
              "id": "actions/code-injection",
              "name": "Code injection",
              "shortDescription": {"text": "Code injection"},
              "fullDescription": {"text": "Untrusted input reaches a run step."},
              "properties": {
                "kind": "path-problem",
                "problem.severity": "error",
                "security-severity": "9.3",
                "tags": ["security", "external/cwe/cwe-094"]
              }
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "actions/code-injection",
          "level": "error",
          "message": {"text": "Untrusted input"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": ".github/workflows/main.yml"},
                "region": {
                  "startLine": 12, "startColumn": 9,
                  "endLine": 12, "endColumn": 40,
                  "snippet": {"text": "run: echo ${{ github.event.issue.title }}\n"}
                }
              }
            }
          ],
          "codeFlows": [
            {
              "threadFlows": [
                {
                  "locations": [
                    {"location": {"physicalLocation": {
                      "artifactLocation": {"uri": ".github/workflows/main.yml"},
                      "region": {"snippet": {"text": "github.event.issue.title"}}}}},
                    {"location": {"physicalLocation": {
                      "artifactLocation": {"uri": ".github/workflows/main.yml"},
                      "region": {"snippet": {"text": "env.TITLE"}}}}},
                    {"location": {"physicalLocation": {
                      "artifactLocation": {"uri": ".github/workflows/main.yml"},
                      "region": {"snippet": {"text": "run: echo $TITLE"}}}}}
                  ]
                }
              ]
            }
          ],
          "partialFingerprints": {"primaryLocationLineHash": "abc123"}
        },
        {
          "ruleId": "actions/code-injection",
          "message": {"text": "Second finding"},
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {"uri": ".github/workflows/other.yml"},
                "region": {"startLine": 3}
              }
            }
          ]
        }
      ]
    }
  ]
}`

func collect(t *testing.T, doc string) ([]Rule, []Result) {
	t.Helper()
	var rules []Rule
	var results []Result
	err := Stream(strings.NewReader(doc), Handler{
		OnRule:   func(r Rule) error { rules = append(rules, r); return nil },
		OnResult: func(r Result) error { results = append(results, r); return nil },
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return rules, results
}

func TestStreamRules(t *testing.T) {
	rules, _ := collect(t, codeFlowSarif)

	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	got := rules[0]
	if got.ID != "actions/code-injection" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Kind != "path-problem" {
		t.Errorf("Kind = %q, want path-problem", got.Kind)
	}
	if got.Severity != "error" {
		t.Errorf("Severity = %q, want error", got.Severity)
	}
	if got.SecuritySeverity != "9.3" {
		t.Errorf("SecuritySeverity = %q, want 9.3", got.SecuritySeverity)
	}
	if got.Description != "Untrusted input reaches a run step." {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "security" {
		t.Errorf("Tags = %v", got.Tags)
	}
}

// Source and sink snippets are the thread-flow endpoints; sarif-sql left these columns NULL.
func TestStreamResultCodeFlowEndpoints(t *testing.T) {
	_, results := collect(t, codeFlowSarif)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	first := results[0]
	if first.StepCount != 3 {
		t.Errorf("StepCount = %d, want 3", first.StepCount)
	}
	if first.SourceSnippet != "github.event.issue.title" {
		t.Errorf("SourceSnippet = %q", first.SourceSnippet)
	}
	if first.SinkSnippet != "run: echo $TITLE" {
		t.Errorf("SinkSnippet = %q", first.SinkSnippet)
	}
	if first.FilePath != ".github/workflows/main.yml" {
		t.Errorf("FilePath = %q", first.FilePath)
	}
	if first.Region.StartLine != 12 || first.Region.EndColumn != 40 {
		t.Errorf("Region = %+v", first.Region)
	}
	if first.Snippet != "run: echo ${{ github.event.issue.title }}" {
		t.Errorf("Snippet = %q", first.Snippet)
	}
	if first.Fingerprints["primaryLocationLineHash"] != "abc123" {
		t.Errorf("Fingerprints = %v", first.Fingerprints)
	}
}

func TestStreamResultWithoutCodeFlow(t *testing.T) {
	_, results := collect(t, codeFlowSarif)

	second := results[1]
	if second.StepCount != 0 || second.SourceSnippet != "" || second.SinkSnippet != "" {
		t.Errorf("expected no flow data, got %+v", second)
	}
	if second.Region.StartLine != 3 {
		t.Errorf("StartLine = %d, want 3", second.Region.StartLine)
	}
}

// `--sarif-group-rules-by-pack` moves rules from tool.driver into tool.extensions.
func TestStreamRulesFromExtensions(t *testing.T) {
	const doc = `{"runs":[{"tool":{"driver":{"name":"CodeQL"},"extensions":[
		{"rules":[{"id":"ext/rule","properties":{"kind":"problem"}}]}]},"results":[]}]}`

	rules, _ := collect(t, doc)
	if len(rules) != 1 || rules[0].ID != "ext/rule" {
		t.Fatalf("got %+v, want the extension rule", rules)
	}
	if rules[0].Name != "ext/rule" {
		t.Errorf("Name = %q, want the id as fallback", rules[0].Name)
	}
}

// Results must reach the handler as they decode, so a huge file never has to fit in memory.
func TestStreamDeliversResultsIncrementally(t *testing.T) {
	stop := errors.New("stop")
	seen := 0
	err := Stream(strings.NewReader(codeFlowSarif), Handler{
		OnResult: func(Result) error {
			seen++
			return stop
		},
	})
	if !errors.Is(err, stop) {
		t.Fatalf("got %v, want the handler's sentinel", err)
	}
	if seen != 1 {
		t.Errorf("handler ran %d times, want 1", seen)
	}
}

func TestStreamHandlesResultsBeforeTool(t *testing.T) {
	const doc = `{"runs":[{"results":[{"ruleId":"a","message":{"text":"m"}}],
		"tool":{"driver":{"rules":[{"id":"a"}]}}}]}`

	rules, results := collect(t, doc)
	if len(results) != 1 || results[0].RuleID != "a" {
		t.Errorf("results = %+v", results)
	}
	if len(rules) != 1 {
		t.Errorf("rules = %+v", rules)
	}
}

func TestStreamNoRunsIsAnError(t *testing.T) {
	if err := Stream(strings.NewReader(`{"version":"2.1.0"}`), Handler{}); !errors.Is(err, ErrNoRuns) {
		t.Errorf("got %v, want ErrNoRuns", err)
	}
}

func TestStreamTruncatedIsAnError(t *testing.T) {
	const doc = `{"runs":[{"results":[{"ruleId":"a"},`
	if err := Stream(strings.NewReader(doc), Handler{OnResult: func(Result) error { return nil }}); err == nil {
		t.Error("expected an error for truncated input")
	}
}

func TestStreamNilHandlersAreSafe(t *testing.T) {
	if err := Stream(strings.NewReader(codeFlowSarif), Handler{}); err != nil {
		t.Errorf("Stream with no callbacks: %v", err)
	}
}

func TestStreamMultipleRuns(t *testing.T) {
	const doc = `{"runs":[
		{"tool":{"driver":{"rules":[{"id":"r1"}]}},"results":[{"ruleId":"r1"}]},
		{"tool":{"driver":{"rules":[{"id":"r2"}]}},"results":[{"ruleId":"r2"},{"ruleId":"r2"}]}]}`

	rules, results := collect(t, doc)
	if len(rules) != 2 {
		t.Errorf("got %d rules, want 2", len(rules))
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
}
