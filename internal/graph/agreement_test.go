package graph

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// partNamed finds one comparison by name, failing the test when it was not made at all.
func partNamed(t *testing.T, parts []store.CheckPart, name string) store.CheckPart {
	t.Helper()
	for _, p := range parts {
		if p.Part == name {
			return p
		}
	}
	t.Fatalf("no part named %q among %+v", name, parts)
	return store.CheckPart{}
}

func closureAgreement(t *testing.T, s *store.Store, repositories ...string) []store.CheckPart {
	t.Helper()
	parts, err := ClosureAgreement(s, repositories)
	if err != nil {
		t.Fatalf("ClosureAgreement: %v", err)
	}
	return parts
}

// A reach row the closure cannot account for is the walk admitting what the repository does not run.
func TestCheck8CatchesAReachRowTheClosureDoesNotCarry(t *testing.T) {
	f := toolFixture(t, true)
	rebuilt(t, f.s)
	clean := closureAgreement(t, f.s, "acme/app", "acme/tool")
	if partNamed(t, clean, closureReachPart).Compared == 0 {
		t.Fatal("nothing was compared, so the planted row below would be caught vacuously")
	}
	for _, p := range clean {
		for _, d := range p.Disagreements {
			t.Errorf("before planting, %s disagrees about %s: %s / %s", p.Part, d.Subject, d.Claim,
				d.Independent)
		}
	}

	exec(t, f.s, `INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops)
		SELECT n.row_id, r.row_id, 1 FROM abom_node_live n, repository r
		 WHERE n.node_key = 'actions/cache' AND r.repository_full_name = 'acme/app'`)

	got := partNamed(t, closureAgreement(t, f.s, "acme/app", "acme/tool"), closureReachPart)
	if len(got.Disagreements) != 1 || got.Disagreements[0].Subject != "acme/app -> actions/cache" {
		t.Errorf("the planted reach row is reported as %+v, want acme/app -> actions/cache alone",
			got.Disagreements)
	}
	walk, err := (&Builder{Store: f.s}).ExposureWalkAgreement()
	if err != nil {
		t.Fatalf("ExposureWalkAgreement: %v", err)
	}
	if d := partNamed(t, walk, walkReachPart).Disagreements; len(d) != 1 ||
		d[0].Subject != "actions/cache" {
		t.Errorf("the exposure walk comparison reports %+v, want actions/cache alone", d)
	}
}

// An exposure the closure carries at no starting commit is two walks disagreeing on one supply chain.
func TestCheck8CatchesAnExposureTheClosureDoesNotCarry(t *testing.T) {
	f := toolFixture(t, true)
	rebuilt(t, f.s)
	exec(t, f.s, `INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		SELECT r.row_id, n.row_id, 'GHSA-planted', 1, '[]' FROM abom_node_live n, repository r
		 WHERE n.node_key = 'actions/cache' AND r.repository_full_name = 'acme/app'`)

	got := partNamed(t, closureAgreement(t, f.s, "acme/app"), closureExposurePart)
	if got.Compared == 0 || len(got.Disagreements) != 1 ||
		got.Disagreements[0].Subject != "acme/app -> actions/cache" {
		t.Errorf("the planted exposure is reported as %+v after comparing %d, want acme/app -> "+
			"actions/cache alone", got.Disagreements, got.Compared)
	}
}

// GitHub names accounts without regard to case, so a lower-case key and an owner as written agree.
func TestCheck8ComparesNodesWhateverCaseTheirOwnerIsWrittenIn(t *testing.T) {
	f := toolFixture(t, true)
	exec(t, f.s, `UPDATE abom_node SET owner = 'Actions' WHERE node_key = 'actions/checkout'`)
	rebuilt(t, f.s)

	got := partNamed(t, closureAgreement(t, f.s, "acme/tool"), closureReachPart)
	if got.Compared == 0 {
		t.Fatal("nothing was compared, so agreement would be vacuous")
	}
	for _, d := range got.Disagreements {
		t.Errorf("%s is reported as a disagreement: %s / %s", d.Subject, d.Claim, d.Independent)
	}
}

// What only a protected branch declares is reach's and not the snapshot's, so it is excluded by rule.
func TestCheck8ExcludesWhatOnlyAProtectedBranchDeclaresByRule(t *testing.T) {
	f := toolFixture(t, true)
	f.queued(t, "acme/tool", "release", 0, "p1")
	f.declares(t, "acme/tool", "p1", ".github/workflows/release.yml", 3, "actions/upload-artifact", "v4")
	rebuilt(t, f.s)

	got := partNamed(t, closureAgreement(t, f.s, "acme/tool"), closureReachPart)
	for _, d := range got.Disagreements {
		t.Errorf("%s is reported as a disagreement: %s / %s", d.Subject, d.Claim, d.Independent)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Count != 1 ||
		!strings.Contains(got.Skipped[0].Reason, "protected branch") {
		t.Errorf("skipped %+v, want actions/upload-artifact excluded by the protected-branch rule",
			got.Skipped)
	}
}
