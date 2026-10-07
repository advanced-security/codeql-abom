package store

import "testing"

// takenLane is a findings lane whose unpinned-tag rule code scanning's analysis answered, under the
// pack release and CodeQL that analysis ran, and whose other rule ABOM evaluated.
func takenLane(t *testing.T, s *Store) (unpinned, secrets int64) {
	t.Helper()
	lane := sourcedRun(t, s, QueryRun{
		QueryID: "abom-findings.qls", QueryVersion: "lane-v1", Pack: upstreamFindings + " " + abomFindings,
		Repository: "octo-org/app", DBCommit: "c-app", CLIVersion: "2.27.1", RecordedBy: RecordedByCrawl,
		Rules: []QueryRunRule{
			{RuleID: "actions/unpinned-tag", Pack: upstreamFindings, PackVersion: "0.6.9+6e9f9e38",
				CLIVersion: "2.23.1", TakenFrom: "github-code-scanning:1895743852"},
			{RuleID: "abom/secrets-inherit", QueryVersion: "a1", Pack: abomFindings,
				PackVersion: "2.27.1", PackDigest: "d-abom"},
		},
	})
	seedFindings(t, s, repoID(t, s, "octo-org/app"), ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", QueryRunID: lane, Provider: "CodeQL"},
		Finding{RuleID: "abom/secrets-inherit", QueryRunID: lane, Provider: "CodeQL"},
	)
	return findingRow(t, s, "octo-org/app", "actions/unpinned-tag"),
		findingRow(t, s, "octo-org/app", "abom/secrets-inherit")
}

func onlyEvaluation(t *testing.T, s *Store, finding int64) FindingEvaluation {
	t.Helper()
	got, err := s.FindingTrace(finding)
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if len(got.Evaluations) != 1 {
		t.Fatalf("traced to %+v, want the lane alone", got.Evaluations)
	}
	return got.Evaluations[0]
}

// A rule code scanning's analysis answered is traced to what produced it: the analysis, its pack
// release and its CodeQL, and no query version, since none was read. Claiming the lane's own would
// put another release's results under ABOM's name; the lane's other rules keep ABOM's.
func TestARuleTakenFromCodeScanningIsTracedToWhatProducedIt(t *testing.T) {
	s := subgraphFixture(t)
	unpinned, secrets := takenLane(t, s)

	taken := onlyEvaluation(t, s, unpinned)
	if taken.TakenFrom != "github-code-scanning:1895743852" || taken.CLIVersion != "2.23.1" ||
		taken.Pack != upstreamFindings || taken.PackVersion != "0.6.9+6e9f9e38" || taken.RuleVersion != "" ||
		taken.Source != QuerySourceUpstream {
		t.Errorf("the taken rule reads %+v", taken)
	}
	own := onlyEvaluation(t, s, secrets)
	if own.TakenFrom != "" || own.CLIVersion != "2.27.1" || own.RuleVersion != "a1" {
		t.Errorf("ABOM's rule reads %+v", own)
	}
}

// An expansion runs for weeks, so a database from before rules kept their producer gains it in place.
func TestAnUpgradedDatabaseTracesATakenRule(t *testing.T) {
	s := migratedLegacyStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "octo-org", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	if _, err := s.InsertOwnedRepository("octo-org/app"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}
	unpinned, _ := takenLane(t, s)

	if taken := onlyEvaluation(t, s, unpinned); taken.TakenFrom == "" || taken.CLIVersion != "2.23.1" {
		t.Errorf("on an upgraded database the taken rule reads %+v", taken)
	}
}
