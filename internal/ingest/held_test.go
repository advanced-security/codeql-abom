package ingest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Held results are read the way an ingest reads, so converting one writes exactly the finding
// adopting the query would have stored. Compared against a real ingest of the same CLI output
// rather than against expectations restated here.
func TestHeldResultsAreWhatAnIngestWouldStore(t *testing.T) {
	held, err := HeldFromSARIF(readFixture(t, "marker-findings.sarif"),
		Options{Repository: "octo-org/marked"})
	if err != nil {
		t.Fatalf("HeldFromSARIF: %v", err)
	}

	s := newTestStore(t)
	if _, err := New(s, store.NewWriter(s, 0)).FindingsFromSARIF(
		readFixture(t, "marker-findings.sarif"), Options{Repository: "octo-org/marked"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	stored := map[string]bool{}
	rows, err := s.DB().Query(`
		SELECT COALESCE(provider, ''), rule_id, COALESCE(file_path, ''), COALESCE(start_line, 0),
		       COALESCE(start_column, 0), COALESCE(end_line, 0), COALESCE(end_column, 0),
		       COALESCE(message, ''), COALESCE(severity, ''), security_severity,
		       COALESCE(fingerprint, ''), COALESCE(result_hash, '')
		FROM abom_finding`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var f store.Finding
		if err := rows.Scan(&f.Provider, &f.RuleID, &f.FilePath, &f.StartLine, &f.StartColumn,
			&f.EndLine, &f.EndColumn, &f.Message, &f.Severity, &f.SecuritySeverity, &f.Fingerprint,
			&f.ResultHash); err != nil {
			t.Fatal(err)
		}
		stored[findingKey(f)] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(held) == 0 || len(held) != len(stored) {
		t.Fatalf("held %d results where the ingest stored %d", len(held), len(stored))
	}
	for _, h := range held {
		if h.RepositoryID != 0 || h.QueryRunID != 0 {
			t.Errorf("a held result names repository %d and evaluation %d before anybody converted it",
				h.RepositoryID, h.QueryRunID)
		}
		if !stored[findingKey(h)] {
			t.Errorf("held %+v, which the ingest did not store", h)
		}
	}
}

// findingKey compares a finding by what it says, so an absent score equals an absent score.
func findingKey(f store.Finding) string {
	score := "none"
	if f.SecuritySeverity != nil {
		score = fmt.Sprint(*f.SecuritySeverity)
	}
	f.SecuritySeverity = nil
	return fmt.Sprintf("%+v|%s", f, score)
}

// Held to the ingest's own refusals: a document that searched for nothing holds nothing rather
// than a clean organization, and one another CodeQL wrote is not this CodeQL's answer.
func TestHoldingRefusesWhatAnIngestRefuses(t *testing.T) {
	const empty = `{"runs":[{"tool":{"driver":{"name":"CodeQL","rules":[]}},"results":[]}]}`
	if _, err := HeldFromSARIF(strings.NewReader(empty),
		Options{Repository: "octo-org/app"}); err == nil || !strings.Contains(err.Error(), "octo-org/app") {
		t.Errorf("a document naming no rules was held from (%v)", err)
	}
	if _, err := HeldFromSARIF(readFixture(t, "marker-findings.sarif"),
		Options{Repository: "octo-org/marked", CLIVersion: "2.26.4"}); err == nil {
		t.Error("a document written by another CodeQL was held as this one's")
	}
}
