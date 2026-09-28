package store

import (
	"database/sql"
	"fmt"
)

// How a CodeQL database was built, which decides what a result from it means.
//
// Every database ABOM has built is one repository's own sparse checkout — the shape GitHub's
// Default Setup builds too — so a rule firing in one is reporting something a developer could also
// be shown by their own code scanning. A database holding an owned repository *and* its staged
// transitive closure answers a different question with the same rule ids: a result there is
// exposure reached through a `uses:` boundary, which nothing native reports.
//
// The mode belongs to the run because it is a fact about the database, which is the same reason
// `cli_version` lives there. Recording it on the query, the suite or the pack would make it
// un-askable for exactly the rule that most needs it, since one query id runs in both shapes.
const (
	// AnalysisModeRegular is one repository's own checkout.
	AnalysisModeRegular = "regular"
	// AnalysisModeExtended is an owned repository plus its staged closure.
	AnalysisModeExtended = "extended"
	// AnalysisModeAnyShape is a deliberate union of both, which a reader has to ask for. It is
	// not a mode a run can be recorded in and it is not in the filter language's vocabulary: a
	// count spanning two shapes has to say it is mixed, and nothing may arrive at one by default.
	AnalysisModeAnyShape = "mixed"
)

// AnalysisModes is the whole vocabulary, in the order a reader should be offered it: the shape
// every existing row has, then the one that sees past a repository boundary.
func AnalysisModes() []string { return []string{AnalysisModeRegular, AnalysisModeExtended} }

// ValidAnalysisMode reports whether a writer stated a shape this build knows how to aggregate
// over.
//
// Empty is deliberately not valid. A row whose writer did not state a mode cannot be told apart
// from one that did, and its wrong value would be indistinguishable from a right one — the same
// absent-is-not-zero rule that keeps a null score out of the arithmetic.
func ValidAnalysisMode(mode string) bool {
	return mode == AnalysisModeRegular || mode == AnalysisModeExtended
}

// analysisModeKeyPart is what a mode contributes to a run's identity.
//
// Regular contributes nothing, so pre-mode run keys still name their original regular database.
// Anything else is a different database and has to be a different key.
func analysisModeKeyPart(mode string) string {
	if mode == AnalysisModeRegular {
		return ""
	}
	return mode
}

// analysisModeViewDDL is applied by Migrate, after the mode column exists and after the live
// views the edge ones build on.
//
// Views rather than a column on every edge and finding row, for a reason that is about
// correctness before cost. An edge is keyed by its source location, so a closure database
// re-observing an owned repository's own `uses:` line produces *the same row*; a finding is keyed
// by rule, location and fingerprint, and the issue this implements requires that one defect seen
// in two shapes stay one defect. A denormalized column would have to hold one value for a row two
// shapes saw, so whichever lane wrote last would decide which totals the row joined. Reading the
// mode through the run instead costs a lookup on an INTEGER PRIMARY KEY, which is a rowid seek.
//
// The regular views admit a row whose provenance is absent: `abom_edge.query_run_id` is nullable.
// Absent is not extended; treating it as a third mode would drop historical regular rows from the
// totals this change is required to leave byte-identical.
const analysisModeViewDDL = `
INSERT OR IGNORE INTO abom_edge_sighting (edge_row_id, query_run_id)
SELECT row_id, query_run_id FROM abom_edge_live WHERE query_run_id IS NOT NULL;

INSERT OR IGNORE INTO abom_finding_sighting (finding_row_id, query_run_id)
SELECT row_id, query_run_id FROM abom_finding WHERE query_run_id IS NOT NULL;

CREATE VIEW IF NOT EXISTS abom_edge_any_mode AS SELECT * FROM abom_edge_live;

DROP VIEW IF EXISTS abom_edge_regular;
CREATE VIEW abom_edge_regular AS
SELECT e.* FROM abom_edge_live e
WHERE EXISTS (SELECT 1 FROM abom_edge_sighting s
              JOIN abom_query_run q ON q.row_id = s.query_run_id
              WHERE s.edge_row_id = e.row_id AND q.analysis_mode = 'regular')
   OR COALESCE(
        (SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = e.query_run_id),
        'regular') = 'regular';

DROP VIEW IF EXISTS abom_edge_extended;
CREATE VIEW abom_edge_extended AS
SELECT e.* FROM abom_edge_live e
WHERE EXISTS (SELECT 1 FROM abom_edge_sighting s
              JOIN abom_query_run q ON q.row_id = s.query_run_id
              WHERE s.edge_row_id = e.row_id AND q.analysis_mode = 'extended')
   OR (SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = e.query_run_id) = 'extended';

CREATE VIEW IF NOT EXISTS abom_finding_any_mode AS SELECT * FROM abom_finding;

-- Through the sighting relation rather than abom_finding.query_run_id, which holds whichever run
-- most recently reproduced the finding. One defect reported by both shapes is one row, and which
-- lane ran last must not decide which totals it joins.
DROP VIEW IF EXISTS abom_finding_regular;
CREATE VIEW abom_finding_regular AS
SELECT f.* FROM abom_finding f
WHERE EXISTS (SELECT 1 FROM abom_finding_sighting s
              JOIN abom_query_run q ON q.row_id = s.query_run_id
              WHERE s.finding_row_id = f.row_id AND q.analysis_mode = 'regular')
   OR COALESCE((SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = f.query_run_id),
               'regular') = 'regular';

DROP VIEW IF EXISTS abom_finding_extended;
CREATE VIEW abom_finding_extended AS
SELECT f.* FROM abom_finding f
WHERE EXISTS (SELECT 1 FROM abom_finding_sighting s
			  JOIN abom_query_run q ON q.row_id = s.query_run_id
			  WHERE s.finding_row_id = f.row_id AND q.analysis_mode = 'extended')
   OR (SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = f.query_run_id) = 'extended';`

// edgeSource and findingSource name the view a read should use for a stated mode.
//
// A read that interpolates a table name escapes the source-enumerating guard, so these two are the
// only functions allowed to do it and TestTheModeSourcesNameOnlyDeclaredViews pins what they can
// return. Every other read spells a view outright.
//
// An empty mode is not a third shape. It is a caller that did not ask, and the answer to not
// asking is `regular` — the shape every aggregate on record reads, and the one that keeps an
// unasked question answering exactly what it answered before the term existed.
func edgeSource(mode string) string {
	switch mode {
	case AnalysisModeExtended:
		return "abom_edge_extended"
	case AnalysisModeAnyShape:
		return "abom_edge_any_mode"
	default:
		return "abom_edge_regular"
	}
}

func findingSource(mode string) string {
	switch mode {
	case AnalysisModeExtended:
		return "abom_finding_extended"
	case AnalysisModeAnyShape:
		return "abom_finding_any_mode"
	default:
		return "abom_finding_regular"
	}
}

func modeNodeIDs(mode string) string {
	return `WITH observed(row_id) AS (
		SELECT src_node_row_id FROM ` + edgeSource(mode) + `
		UNION SELECT dst_node_row_id FROM ` + edgeSource(mode) + `
		UNION SELECT fn.node_row_id FROM abom_finding_node fn
		JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
	)
	SELECT row_id FROM observed
	UNION SELECT container_node_row_id FROM abom_node_live
	WHERE row_id IN (SELECT row_id FROM observed) AND container_node_row_id IS NOT NULL`
}

func nodeFindingCount(mode, nodeID string) string {
	return `(SELECT COUNT(*) FROM abom_finding_node fn
		JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
		WHERE fn.node_row_id = ` + nodeID + `)`
}

func nodeModeFilter(mode string) string {
	switch mode {
	case AnalysisModeAnyShape:
		return ""
	case AnalysisModeExtended:
		return " AND n.row_id IN (" + modeNodeIDs(AnalysisModeExtended) + ")"
	default:
		return ` AND (NOT EXISTS (SELECT 1 FROM abom_query_run WHERE analysis_mode = 'extended')
			OR n.row_id NOT IN (SELECT row_id FROM (` + modeNodeIDs(AnalysisModeExtended) + `)
			EXCEPT SELECT row_id FROM (` + modeNodeIDs(AnalysisModeRegular) + `)))`
	}
}

func guardEdgeAnalysisMode(tx *sql.Tx, edge Edge, targetID int64) error {
	var conflict bool
	err := tx.QueryRow(`WITH incoming(mode) AS (
		SELECT COALESCE((SELECT analysis_mode FROM abom_query_run WHERE row_id = ?), 'regular')
	)
	SELECT EXISTS (SELECT 1 FROM abom_edge_live e, incoming
		WHERE e.edge_key = ?
		  AND (e.dst_node_row_id <> ? OR e.ref_raw IS NOT ? OR e.ref_value IS NOT ?
		       OR e.pin_comment IS NOT ?)
		  AND (COALESCE((SELECT analysis_mode FROM abom_query_run WHERE row_id = e.query_run_id),
		                'regular') <> incoming.mode
		       OR EXISTS (SELECT 1 FROM abom_edge_sighting seen
		                  JOIN abom_query_run run ON run.row_id = seen.query_run_id
		                  WHERE seen.edge_row_id = e.row_id AND run.analysis_mode <> incoming.mode)))`,
		edge.QueryRunID, edge.Key, targetID, nullable(edge.RefRaw), nullable(edge.RefValue),
		nullable(edge.PinComment)).Scan(&conflict)
	if err != nil {
		return fmt.Errorf("checking edge analysis mode: %w", err)
	}
	if conflict {
		return fmt.Errorf("edge %q has conflicting observations across analysis modes", edge.Key)
	}
	return nil
}

// edgeModeBroadens reports whether this observation moves an edge already on record into a shape
// it was not a member of.
//
// Asked before the row is written, because both halves of a broadening are invisible afterwards:
// the sighting adds a membership without changing `row_id`, and the upsert can hand the row to a
// run of another shape. A delta selects edges newer than the cursor's watermark, so neither
// reaches a graph somebody already has open — it has to be told to load again instead. A first
// observation broadens nothing: its row is newer than every cursor, so the delta carries it.
func edgeModeBroadens(tx *sql.Tx, edge Edge) (bool, error) {
	if edge.QueryRunID <= 0 {
		return false, nil
	}
	var broadens bool
	err := tx.QueryRow(`WITH incoming(mode) AS (
		SELECT COALESCE((SELECT analysis_mode FROM abom_query_run WHERE row_id = ?), 'regular')
	)
	SELECT EXISTS (SELECT 1 FROM abom_edge_live e, incoming
		WHERE e.edge_key = ?
		  AND COALESCE((SELECT q.analysis_mode FROM abom_query_run q
		                WHERE q.row_id = e.query_run_id), 'regular') <> incoming.mode
		  AND NOT EXISTS (SELECT 1 FROM abom_edge_sighting s
		                  JOIN abom_query_run q ON q.row_id = s.query_run_id
		                  WHERE s.edge_row_id = e.row_id AND q.analysis_mode = incoming.mode))`,
		edge.QueryRunID, edge.Key).Scan(&broadens)
	if err != nil {
		return false, fmt.Errorf("checking whether an edge gains an analysis mode: %w", err)
	}
	return broadens, nil
}

func recordEdgeSighting(tx *sql.Tx, edge Edge) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO abom_edge_sighting (edge_row_id, query_run_id)
		SELECT row_id, ? FROM abom_edge_live WHERE edge_key = ? AND ? > 0`,
		edge.QueryRunID, edge.Key, edge.QueryRunID)
	if err != nil {
		return fmt.Errorf("recording edge sighting: %w", err)
	}
	return nil
}
