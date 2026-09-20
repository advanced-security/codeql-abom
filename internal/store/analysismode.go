package store

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
// Regular contributes nothing, so every run key computed before the column existed still names the
// run it named: all of them described one repository's own checkout, which is what the backfill
// rests on. Anything else is a different database and has to be a different key, or an extended
// evaluation would collide with the regular one and silently take over its row.
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
// The regular views admit a row whose provenance is absent: `abom_edge.query_run_id` is nullable
// and 2,759 corpus findings name no run at all. Absent is not extended — those rows were on
// record before the column existed, all of them from one repository's own checkout, which is the
// documented backfill. Treating absence as a third mode would silently drop them from the totals
// this change is required to leave byte-identical.
const analysisModeViewDDL = `
CREATE VIEW IF NOT EXISTS abom_edge_any_mode AS SELECT * FROM abom_edge_live;

CREATE VIEW IF NOT EXISTS abom_edge_regular AS
SELECT e.* FROM abom_edge_live e
WHERE COALESCE(
        (SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = e.query_run_id),
        'regular') = 'regular';

CREATE VIEW IF NOT EXISTS abom_edge_extended AS
SELECT e.* FROM abom_edge_live e
WHERE (SELECT q.analysis_mode FROM abom_query_run q WHERE q.row_id = e.query_run_id) = 'extended';

CREATE VIEW IF NOT EXISTS abom_finding_any_mode AS SELECT * FROM abom_finding;

-- Through the sighting relation rather than abom_finding.query_run_id, which holds whichever run
-- most recently reproduced the finding. One defect reported by both shapes is one row, and which
-- lane ran last must not decide which totals it joins.
CREATE VIEW IF NOT EXISTS abom_finding_regular AS
SELECT f.* FROM abom_finding f
WHERE EXISTS (SELECT 1 FROM abom_finding_sighting s
              JOIN abom_query_run q ON q.row_id = s.query_run_id
              WHERE s.finding_row_id = f.row_id AND q.analysis_mode = 'regular')
   OR NOT EXISTS (SELECT 1 FROM abom_finding_sighting s WHERE s.finding_row_id = f.row_id);

CREATE VIEW IF NOT EXISTS abom_finding_extended AS
SELECT f.* FROM abom_finding f
WHERE EXISTS (SELECT 1 FROM abom_finding_sighting s
              JOIN abom_query_run q ON q.row_id = s.query_run_id
              WHERE s.finding_row_id = f.row_id AND q.analysis_mode = 'extended');`

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
