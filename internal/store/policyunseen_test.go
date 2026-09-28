package store

import "testing"

// A finding nobody attributed to a node is invisible to the evaluation, because the match runs
// through `abom_finding_node`. The gate cannot refuse what it cannot see, but a reader needs to
// know the answer is incomplete.
func TestASimulationSaysWhatItCouldNotSee(t *testing.T) {
	s := policyFixture(t, unpinned("no-unpinned"))
	var appID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, "octo-org/app").
		Scan(&appID); err != nil {
		t.Fatal(err)
	}
	// A path no node occupies, so linking has nothing to attach it to.
	if _, err := s.AddFindings([]Finding{{
		RepositoryID: appID, RuleID: "actions/unpinned-tag", Provider: "CodeQL",
		Severity: "warning", FilePath: "scripts/release.sh", StartLine: 4,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatal(err)
	}

	out, err := s.EvaluatePolicy("no-unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Unattributed != 1 {
		t.Errorf("Unattributed = %d, want the finding no node carries", out.Unattributed)
	}
	if len(out.UnattributedRepositories) != 1 || out.UnattributedRepositories[0] != "octo-org/app" {
		t.Errorf("UnattributedRepositories = %v, want the repository holding it",
			out.UnattributedRepositories)
	}
}

// With everything attributed, the honest answer is zero and not a warning nobody can act on.
func TestASimulationThatSawEverythingSaysSo(t *testing.T) {
	s := policyFixture(t, unpinned("no-unpinned"))

	out, err := s.EvaluatePolicy("no-unpinned", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Unattributed != 0 || len(out.UnattributedRepositories) != 0 {
		t.Errorf("Unattributed = %d in %v, want nothing missed",
			out.Unattributed, out.UnattributedRepositories)
	}
}
