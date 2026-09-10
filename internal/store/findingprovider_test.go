package store

import (
	"database/sql"
	"testing"
)

// Which producer made a claim is the first thing the finding table cannot say. Everything in
// `db/finding-providers.md` waits on it: a policy finding and a CodeQL finding have to be readable
// through one shape, and a reader has to be able to tell them apart without inspecting which
// provenance columns happen to be filled.
func TestAFindingRecordsWhichProviderProducedIt(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 12, StartColumn: 9,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if got := providerOf(t, s, "actions/unpinned-tag"); got != ProviderCodeQL {
		t.Errorf("provider = %q, want %q", got, ProviderCodeQL)
	}
}

// The `declared_by` rule, applied to findings: a row written before the column existed is
// **unknown**, and relabeling it `codeql` would invent the provenance the column exists to record.
// Every finding on the reference corpus is one of these.
func TestAFindingThatPredatesTheProviderColumnClaimsNone(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 12, StartColumn: 9,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if got := providerOf(t, s, "actions/unpinned-tag"); got != "" {
		t.Errorf("provider = %q, want it unset: nobody recorded one", got)
	}
}

// A finding already on record gains its provider when a provider re-reports it, for the reason
// `query_run_id` does: without it the corpus can never learn what it did not record the first time.
// COALESCE, so a lane carrying no provider cannot erase one.
func TestReingestAttachesTheProviderAndNeverErasesIt(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	anonymous := Finding{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 12, StartColumn: 9,
	}
	if _, err := s.AddFindings([]Finding{anonymous}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	attributed := anonymous
	attributed.Provider = ProviderCodeQL
	if _, err := s.AddFindings([]Finding{attributed}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if got := providerOf(t, s, "actions/unpinned-tag"); got != ProviderCodeQL {
		t.Errorf("provider = %q, want %q after a provider re-reported it", got, ProviderCodeQL)
	}

	if _, err := s.AddFindings([]Finding{anonymous}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if got := providerOf(t, s, "actions/unpinned-tag"); got != ProviderCodeQL {
		t.Errorf("provider = %q, want it kept: a lane carrying none must not erase one", got)
	}
}

// "Every finding names its producer and how to reproduce it." A CodeQL run is reproducible from
// its pack digest and CLI version; a provider this build does not know is not, and saying so is
// the requirement. Unknown is neither — it is the absence of the question, not the answer *no*.
func TestAProviderSaysWhetherItsFindingsCanBeReproduced(t *testing.T) {
	for _, tt := range []struct {
		provider string
		want     bool
	}{
		{ProviderCodeQL, true},
		{"", false},
		{"something-nobody-registered", false},
	} {
		if got := Reproducible(tt.provider); got != tt.want {
			t.Errorf("Reproducible(%q) = %v, want %v", tt.provider, got, tt.want)
		}
	}
	// Absent and not-reproducible must stay distinguishable, or "we did not ask" reads as "it
	// cannot be done".
	if KnownProvider("") {
		t.Error("the empty provider is not a provider")
	}
	if !KnownProvider(ProviderCodeQL) {
		t.Errorf("%q is the provider that exists and must be recognized", ProviderCodeQL)
	}
}

func providerOf(t *testing.T, s *Store, ruleID string) string {
	t.Helper()
	var p sql.NullString
	if err := s.DB().QueryRow(
		`SELECT provider FROM abom_finding WHERE rule_id = ?`, ruleID).Scan(&p); err != nil {
		t.Fatalf("reading the provider of %s: %v", ruleID, err)
	}
	return p.String
}
