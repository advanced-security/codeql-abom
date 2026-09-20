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
