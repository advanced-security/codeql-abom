package ingest

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A prediction reads a document the way an ingest does, so what it says a query would report is
// exactly what adopting the query would store. Compared against a real ingest of the same CLI
// output rather than against expectations restated here.
func TestAPredictionReadsWhatAnIngestWouldStore(t *testing.T) {
	predicted, err := PredictionsFromSARIF(readFixture(t, "marker-findings.sarif"),
		Options{Repository: "octo-org/marked"})
	if err != nil {
		t.Fatalf("PredictionsFromSARIF: %v", err)
	}

	s := newTestStore(t)
	if _, err := New(s, store.NewWriter(s, 0)).FindingsFromSARIF(
		readFixture(t, "marker-findings.sarif"), Options{Repository: "octo-org/marked"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	stored := map[store.PredictedResult]bool{}
	rows, err := s.DB().Query(`
		SELECT rule_id, COALESCE(file_path, ''), COALESCE(start_line, 0), COALESCE(start_column, 0),
		       COALESCE(end_line, 0), COALESCE(end_column, 0), COALESCE(message, ''),
		       COALESCE(severity, ''), COALESCE(fingerprint, ''), COALESCE(result_hash, '')
		FROM abom_finding`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var f store.PredictedResult
		if err := rows.Scan(&f.RuleID, &f.FilePath, &f.StartLine, &f.StartColumn, &f.EndLine,
			&f.EndColumn, &f.Message, &f.Severity, &f.Fingerprint, &f.ResultHash); err != nil {
			t.Fatal(err)
		}
		stored[f] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(predicted) == 0 || len(predicted) != len(stored) {
		t.Fatalf("predicted %d results where the ingest stored %d", len(predicted), len(stored))
	}
	for _, p := range predicted {
		if !stored[p] {
			t.Errorf("predicted %+v, which the ingest did not store", p)
		}
	}
}

// Held to the ingest's own refusals: a document that searched for nothing predicts nothing rather
// than a clean organization, and one another CodeQL wrote is not this CodeQL's prediction.
func TestAPredictionRefusesWhatAnIngestRefuses(t *testing.T) {
	const empty = `{"runs":[{"tool":{"driver":{"name":"CodeQL","rules":[]}},"results":[]}]}`
	if _, err := PredictionsFromSARIF(strings.NewReader(empty),
		Options{Repository: "octo-org/app"}); err == nil || !strings.Contains(err.Error(), "octo-org/app") {
		t.Errorf("a document naming no rules was predicted from (%v)", err)
	}
	if _, err := PredictionsFromSARIF(readFixture(t, "marker-findings.sarif"),
		Options{Repository: "octo-org/marked", CLIVersion: "2.26.4"}); err == nil {
		t.Error("a document written by another CodeQL was predicted from as this one's")
	}
}
