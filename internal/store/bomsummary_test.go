package store

import "testing"

// Every component something references lands in exactly one of not examined, examined with
// findings, and examined with none found, so the three claims sum to the coverage they come from.
func TestASummaryAccountsForEveryComponentInUseOnce(t *testing.T) {
	doc := BOM{
		Roots:       []string{"octo-org/app"},
		GeneratedAt: "2026-09-28T00:00:00Z",
		Components: []BOMComponent{
			{Key: "octo-org/app/.github/workflows/ci.yml", Depth: 0},
			{Key: "actions/checkout", Direct: true, Depth: 1, RefsInUse: 2, RefsAnalyzed: 2,
				Analyzed: true, Findings: []BOMFinding{{RuleID: "actions/unpinned-tag"}}},
			{Key: "vendor/action", Direct: true, Depth: 1, RefsInUse: 1, RefsAnalyzed: 1,
				Analyzed: true},
			{Key: "helper/tool", Depth: 3, RefsInUse: 1},
		},
	}
	doc.Coverage = summarizeCoverage(doc.Components)

	got := SummarizeBOM(doc)
	want := BOMSummary{
		Roots: doc.Roots, GeneratedAt: doc.GeneratedAt, Coverage: doc.Coverage,
		Direct: 2, Indirect: 1, DeepestHop: 3, WithFindings: 1, NoneFound: 1, NotExamined: 1,
	}
	if got.Direct != want.Direct || got.Indirect != want.Indirect || got.DeepestHop != want.DeepestHop ||
		got.WithFindings != want.WithFindings || got.NoneFound != want.NoneFound ||
		got.NotExamined != want.NotExamined || got.Coverage != want.Coverage ||
		got.GeneratedAt != want.GeneratedAt || len(got.Roots) != 1 {
		t.Fatalf("summary %+v, want %+v", got, want)
	}
	if sum := got.WithFindings + got.NoneFound + got.NotExamined; sum != got.Coverage.ComponentsInUse {
		t.Errorf("the three claims sum to %d of %d components in use", sum, got.Coverage.ComponentsInUse)
	}
}
