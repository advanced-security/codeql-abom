package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The experimental suite exists so a repository with nothing wrong with it still produces findings
// ABOM can attribute — that is what lets a seeded corpus exercise the finding paths at all.
// `AbomExperimentalMarker.ql` was tested in isolation and the ingest path against upstream SARIF,
// and until these tests the two had never met: the other SARIF fixture here carries 35 upstream
// rules and no ABOM rule, so nothing proved a marker finding reaches a node.
//
// Both fixtures are real CLI output over the query's own test tree — `codeql database create
// --language=actions`, then `AbomEdges.ql` and `abom-experimental-findings.qls`. Never hand-edit
// them; change the source tree under `ql/actions/findings/test/AbomExperimentalMarker` and
// regenerate.

func readFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("opening %s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func markedCorpus(t *testing.T) *store.Store {
	t.Helper()
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	opts := Options{Repository: "octo-org/marked", Provenance: "bqrs"}
	if _, err := ing.EdgesFromBQRS(readFixture(t, "marker-edges.json"), opts); err != nil {
		t.Fatalf("EdgesFromBQRS: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := ing.FindingsFromSARIF(readFixture(t, "marker-findings.sarif"),
		Options{Repository: opts.Repository}); err != nil {
		t.Fatalf("FindingsFromSARIF: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
	return s
}

// The suite fires on a repository with no security problem in it at all, which is the property
// that makes it usable for seeding: every job and every composite action produces one result.
func TestTheExperimentalSuiteProducesFindingsOnACleanRepository(t *testing.T) {
	s := markedCorpus(t)

	var total int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&total); err != nil {
		t.Fatalf("counting findings: %v", err)
	}
	// Three jobs and one composite action in the fixture tree.
	if total != 4 {
		t.Errorf("recorded %d findings, want 4", total)
	}

	rows, err := s.DB().Query(`SELECT DISTINCT rule_id FROM abom_finding`)
	if err != nil {
		t.Fatalf("reading rules: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		// The prefix is the whole mechanism by which a consumer tells ABOM's own findings from
		// upstream's and can drop them.
		if !strings.HasPrefix(id, "abom/") {
			t.Errorf("rule %q is not ABOM's own, so this fixture is not the experimental suite", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading rules: %v", err)
	}
}

// A known gap, pinned rather than silenced. The marker reaches every job and composite action;
// attribution does not carry it there. Measured on this fixture:
//
//   - a finding on a job lands on the *workflow*, because `LinkFindingsToNodes` matches a node by
//     `path` and only its first pass matches a line — so job granularity survives only where the
//     finding sits on the job's own `uses:` line, as `delegate` does;
//   - a composite action that references nothing is not a node at all, so its finding attaches to
//     nothing. The graph models what a `uses:` line touches, and `greet/action.yml` names none.
//
// The second is the reproducible case of the corpus-wide "findings linked to no node" gap, which
// until now had only ever been counted. Both numbers are expected to change when attribution is
// repaired, and this breaking at that point is the point.
func TestWhereAMarkerFindingCanAndCannotBeAttributed(t *testing.T) {
	s := markedCorpus(t)

	var attributed, orphaned int
	if err := s.DB().QueryRow(`
		SELECT
		  (SELECT COUNT(DISTINCT finding_row_id) FROM abom_finding_node),
		  (SELECT COUNT(*) FROM abom_finding f WHERE NOT EXISTS (
		     SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id))`).
		Scan(&attributed, &orphaned); err != nil {
		t.Fatalf("counting attribution: %v", err)
	}
	if attributed != 3 || orphaned != 1 {
		t.Errorf("attributed %d and orphaned %d, want 3 and 1; attribution changed, so the "+
			"reasoning above needs rewriting rather than this number", attributed, orphaned)
	}

	var orphanPath string
	if err := s.DB().QueryRow(`
		SELECT f.file_path FROM abom_finding f
		WHERE NOT EXISTS (SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)`).
		Scan(&orphanPath); err != nil {
		t.Fatalf("reading the orphan: %v", err)
	}
	if orphanPath != ".github/actions/greet/action.yml" {
		t.Errorf("the unattributed finding is in %s, want the composite action", orphanPath)
	}

	var onWorkflow int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_finding f
		JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		JOIN abom_node n ON n.row_id = fn.node_row_id
		WHERE n.node_kind = 'workflow'`).Scan(&onWorkflow); err != nil {
		t.Fatalf("counting workflow attribution: %v", err)
	}
	if onWorkflow != 2 {
		t.Errorf("%d findings landed on a workflow rather than their job, want 2", onWorkflow)
	}
}
