package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Overlay databases would make a rebuild proportional to what changed, and the CodeQL CLI accepts
// `--overlay-base` for `actions` without complaint — it exits 0 and prints "Successfully created
// database". It also writes `overlayBaseDatabase: false`, because overlay support is declared per
// extractor by `overlay_support_version` in `codeql-extractor.yml` and the actions extractor does
// not declare it. A silent no-op is the worst shape a blocker can take, so it is asserted rather
// than remembered.
//
// This test failing is good news: it means the extractor gained overlay support and
// `plans/db/analysis-ledger.md` has work to unblock.
func TestTheActionsExtractorStillCannotBuildAnOverlayBase(t *testing.T) {
	db := os.Getenv("ABOM_ITEST_OVERLAY_DB")
	if db == "" {
		t.Skip("set ABOM_ITEST_OVERLAY_DB to a database built with --overlay-base to run this test")
	}

	manifest, err := os.ReadFile(filepath.Join(db, "codeql-database.yml"))
	if err != nil {
		t.Fatalf("reading the database manifest: %v", err)
	}
	// A manifest that never mentions the key would pass a substring check for the false value,
	// so absence is failed separately from the value.
	body := string(manifest)
	if !strings.Contains(body, "overlayBaseDatabase:") {
		t.Fatalf("%s names no overlayBaseDatabase key, so this proves nothing", db)
	}
	if strings.Contains(body, "overlayBaseDatabase: true") {
		t.Error("the actions extractor now builds overlay bases; " +
			"unblock the overlay item in plans/db/analysis-ledger.md")
	}
}
