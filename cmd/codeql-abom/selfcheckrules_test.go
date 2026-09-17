package main

import (
	"strings"
	"testing"
)

// A gate names what it is failing on. Counts alone tell an operator that something is wrong and
// leave them to run CodeQL again by hand to find out what, so the summary has to carry the rule
// ids and where each fired.

func TestSummarizeSARIFRecordsWhichRulesFired(t *testing.T) {
	summary, err := summarizeSARIF(strings.NewReader(twoFindingSARIF))
	if err != nil {
		t.Fatalf("summarizeSARIF: %v", err)
	}
	if got := summary.ByRule["actions/unpinned-tag"]; got != 1 {
		t.Errorf("ByRule[actions/unpinned-tag] = %d, want 1", got)
	}
	if got := summary.ByRule["actions/unnecessary-use-of-advanced-config"]; got != 1 {
		t.Errorf("ByRule[actions/unnecessary-use-of-advanced-config] = %d, want 1", got)
	}
	if len(summary.ByRule) != 2 {
		t.Errorf("ByRule has %d rules, want 2", len(summary.ByRule))
	}
}

// A rule that fired is not the same as a rule that was evaluated. Reporting every rule in the
// suite would bury the two that matter under the hundred that found nothing.
func TestSummarizeSARIFOmitsRulesThatFoundNothing(t *testing.T) {
	summary, err := summarizeSARIF(strings.NewReader(noFindingSARIF))
	if err != nil {
		t.Fatalf("summarizeSARIF: %v", err)
	}
	if len(summary.ByRule) != 0 {
		t.Errorf("ByRule = %v, want empty", summary.ByRule)
	}
}

// Without a location the operator knows a rule fired and not where, which is most of the work.
func TestSummarizeSARIFKeepsWhereEachRuleFired(t *testing.T) {
	summary, err := summarizeSARIF(strings.NewReader(twoFindingSARIF))
	if err != nil {
		t.Fatalf("summarizeSARIF: %v", err)
	}
	where := summary.Locations["actions/unpinned-tag"]
	if len(where) != 1 || !strings.Contains(where[0], ".github/workflows/main.yml") {
		t.Errorf("Locations[actions/unpinned-tag] = %v, want the workflow file", where)
	}
	if !strings.Contains(where[0], "10") {
		t.Errorf("Locations[actions/unpinned-tag] = %v, want the line number", where)
	}
}

func TestWriteSelfcheckSummaryNamesTheRulesThatFired(t *testing.T) {
	var out strings.Builder
	writeSelfcheckSummary(&out, selfcheckSummary{
		Rules: 2, Results: 2,
		BySeverity: map[string]int{"warning": 1, "note": 1},
		ByRule:     map[string]int{"go/useless-assignment-to-local": 3, "go/unused-parameter": 1},
		Locations: map[string][]string{
			"go/useless-assignment-to-local": {"internal/store/a.go:12"},
			"go/unused-parameter":            {"internal/serve/b.go:44"},
		},
	})
	got := out.String()
	for _, want := range []string{
		"go/useless-assignment-to-local", "3",
		"go/unused-parameter", "internal/store/a.go:12",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary output = %q, want it to contain %q", got, want)
		}
	}
}

// The rule listing is ordered, so two runs over the same corpus produce the same text and a
// reader can diff them.
func TestWriteSelfcheckSummaryOrdersRulesDeterministically(t *testing.T) {
	summary := selfcheckSummary{
		Rules: 3, Results: 3,
		ByRule: map[string]int{"z/last": 1, "a/first": 1, "m/middle": 1},
	}
	var first, second strings.Builder
	writeSelfcheckSummary(&first, summary)
	writeSelfcheckSummary(&second, summary)
	if first.String() != second.String() {
		t.Errorf("two writes disagreed:\n%q\n%q", first.String(), second.String())
	}
	got := first.String()
	if strings.Index(got, "a/first") > strings.Index(got, "m/middle") ||
		strings.Index(got, "m/middle") > strings.Index(got, "z/last") {
		t.Errorf("rules are not in order: %q", got)
	}
}
