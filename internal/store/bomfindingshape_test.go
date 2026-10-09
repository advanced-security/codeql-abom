package store

import (
	"slices"
	"testing"
)

// A reader looking at a finding must be able to see which analysis shape produced it without
// first discovering that a filter exists. The document carries CodeQL's other provenance for the
// same reason — a claim whose shape is unstated reads as the regular one, and the whole point of
// the column is that those two are not interchangeable.
func TestAFindingReportsTheShapeItWasSeenIn(t *testing.T) {
	s := bomFixture(t)

	const traced = "octo-org/app/.github/workflows/ci.yml"
	findings, err := s.componentFindings(map[string]int{traced: 0})
	if err != nil {
		t.Fatalf("componentFindings: %v", err)
	}
	got := findings[traced]
	if len(got) == 0 {
		t.Fatal("the fixture's analyzed component reported no findings")
	}
	for _, f := range got {
		if len(f.AnalysisModes) == 0 {
			t.Errorf("%s states no analysis shape", f.RuleID)
		}
		for _, mode := range f.AnalysisModes {
			if !ValidAnalysisMode(mode) {
				t.Errorf("%s states %q, which is not a shape anything can be built in",
					f.RuleID, mode)
			}
		}
	}
}

// A finding no run claims has no shape to state; reporting `regular` would be a guess dressed as
// provenance.
func TestAFindingWithNoRunStatesNoShape(t *testing.T) {
	s := bomFixture(t)

	const untraced = "actions/checkout"
	findings, err := s.componentFindings(map[string]int{untraced: 1})
	if err != nil {
		t.Fatalf("componentFindings: %v", err)
	}
	if len(findings[untraced]) == 0 {
		t.Fatal("the fixture's untraced finding reached no component")
	}
	for _, f := range findings[untraced] {
		if f.RunKey != "" {
			continue
		}
		if len(f.AnalysisModes) != 0 {
			t.Errorf("a finding naming no run claimed the shapes %v", f.AnalysisModes)
		}
	}
}

// One defect seen through two shapes is one defect. Reporting it twice would turn a difference in
// how it was looked for into a difference in what is wrong, and every count built on the document
// would double.
func TestADefectSeenInBothShapesIsOneFindingLabelledWithBoth(t *testing.T) {
	s := bomFixture(t)

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	extended, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeExtended,
		QueryID: "abom/edges", Repository: "octo-org/app", DBCommit: "cafe1234",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddFindings([]Finding{
		{Provider: ProviderCodeQL, QueryRunID: extended, RuleID: "actions/unpinned-tag",
			RepositoryID: app, FilePath: ".github/workflows/ci.yml", StartLine: 1,
			Severity: "warning", SecuritySeverity: score(5.5), Message: "unpinned", Fingerprint: "f1",
			ResultHash: "sha256:result"},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	const traced = "octo-org/app/.github/workflows/ci.yml"
	findings, err := s.componentFindings(map[string]int{traced: 0})
	if err != nil {
		t.Fatalf("componentFindings: %v", err)
	}
	matched := []BOMFinding{}
	for _, f := range findings[traced] {
		if f.RuleID == "actions/unpinned-tag" && f.FilePath == ".github/workflows/ci.yml" {
			matched = append(matched, f)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("one defect seen in two shapes became %d rows in the document", len(matched))
	}
	want := []string{AnalysisModeExtended, AnalysisModeRegular}
	if !slices.Equal(matched[0].AnalysisModes, want) {
		t.Errorf("AnalysisModes = %v, want %v", matched[0].AnalysisModes, want)
	}
}
