package ingest

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func hashesFor(t *testing.T, s *store.Store, repo string) []string {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT COALESCE(f.result_hash,'') FROM abom_finding f
		JOIN repository r ON r.row_id = f.repository_row_id
		WHERE r.repository_full_name = ? ORDER BY f.rule_id`, repo)
	if err != nil {
		t.Fatalf("reading result hashes: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading result hashes: %v", err)
	}
	return out
}

// Every stored finding has to carry the hash, or the chain has a hole exactly where the results
// are and "end to end" oversells.
func TestEveryStoredFindingCarriesAResultHash(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}

	got := hashesFor(t, s, "octo-org/app")
	if len(got) != 2 {
		t.Fatalf("stored %d findings, want 2", len(got))
	}
	for i, h := range got {
		if h == "" {
			t.Errorf("finding %d carries no result hash", i)
		}
	}
	if got[0] == got[1] {
		t.Error("two different findings share a hash")
	}
}

// The same defect found in two repositories is one defect. Storing it as (repository, hash) is
// what turns "how far does this reach" into a group-by.
func TestTheSameFindingInTwoRepositoriesSharesItsHash(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	for _, repo := range []string{"octo-org/app", "octo-org/other"} {
		if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
			Options{Repository: repo}); err != nil {
			t.Fatalf("FindingsFromSARIF(%s): %v", repo, err)
		}
	}

	app, other := hashesFor(t, s, "octo-org/app"), hashesFor(t, s, "octo-org/other")
	if len(app) != 2 || len(other) != 2 {
		t.Fatalf("stored %d and %d findings, want 2 each", len(app), len(other))
	}
	for i := range app {
		if app[i] != other[i] {
			t.Errorf("finding %d hashed %s in one repository and %s in the other",
				i, app[i], other[i])
		}
	}
}

// The SARIF states which CodeQL wrote it. Recording a run as one version while the results came
// from another is the exact lie the identity exists to prevent, so the two are compared rather
// than assumed to agree.
func TestSarifFromADifferentCodeQLIsRefused(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	_, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app", CLIVersion: "2.26.4"})
	if err == nil {
		t.Fatal("SARIF written by an unstated CodeQL was accepted against 2.26.4")
	}
	if !strings.Contains(err.Error(), "2.26.4") {
		t.Errorf("error %q does not name the version that was expected", err)
	}
}

func TestSarifFromTheExpectedCodeQLIsAccepted(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	versioned := strings.Replace(suiteSarif,
		`"name": "CodeQL",`, `"name": "CodeQL", "semanticVersion": "2.26.4",`, 1)
	if versioned == suiteSarif {
		t.Fatal("the fixture no longer has the driver name the test patches")
	}

	if _, err := ing.FindingsFromSARIF(strings.NewReader(versioned),
		Options{Repository: "octo-org/app", CLIVersion: "2.26.4"}); err != nil {
		t.Fatalf("matching versions were refused: %v", err)
	}
}

// Nothing states the version in the single-query lane, and refusing there would stop the crawl
// over a check that was never asked for.
func TestNoExpectedVersionMeansNoCrossCheck(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromSARIF(strings.NewReader(suiteSarif),
		Options{Repository: "octo-org/app"}); err != nil {
		t.Fatalf("ingestion without an expected version failed: %v", err)
	}
}
