package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// pack writes a minimal pack manifest and the suites it declares, so the tests exercise the
// same layout `codeql pack ls` sees rather than a directory of loose files.
func pack(t *testing.T, root, dir, name, suiteDir string, suites ...string) {
	t.Helper()
	packDir := filepath.Join(root, dir)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", packDir, err)
	}
	manifest := "name: " + name + "\nversion: 2.26.4\n"
	if suiteDir != "" {
		manifest += "suites: " + suiteDir + "\n"
	}
	if err := os.WriteFile(filepath.Join(packDir, "codeql-pack.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if suiteDir == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(packDir, suiteDir), 0o755); err != nil {
		t.Fatalf("creating suite dir: %v", err)
	}
	for _, s := range suites {
		if err := os.WriteFile(filepath.Join(packDir, suiteDir, s), []byte("- query: X.ql\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", s, err)
		}
	}
}

// The set of queries ABOM runs is meant to grow without a release, so what a tier may be bound
// to is read from disk rather than compiled in.
func TestASuiteAddedToAPackIsOffered(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/graph-extraction/src", "advanced-security/abom-actions-graph-extraction-src",
		"codeql-suites", "abom-dependency-graph.qls", "invented-today.qls")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}

	want := []string{
		"advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls",
		"advanced-security/abom-actions-graph-extraction-src:codeql-suites/invented-today.qls",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want both suites", got)
	}
	// Sorted, so a control built from this does not reshuffle between reads.
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A suite is named by the pack that provides it, not by where the repository happens to keep the
// file. That identity is what survives both a directory move and publication.
func TestASuiteIsNamedByItsPackRatherThanItsPath(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/findings/src", "advanced-security/abom-actions-findings-src",
		"codeql-suites", "abom-findings.qls")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}

	want := "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %v, want %q", got, want)
	}
}

func TestOnlySuiteFilesAreOffered(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/graph-extraction/src", "advanced-security/abom-actions-graph-extraction-src",
		"codeql-suites", "real.qls", "AbomEdges.ql", "notes.md")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}

	if len(got) != 1 || filepath.Base(got[0]) != "real.qls" {
		t.Errorf("got %v, want only the suite; a query is not a suite", got)
	}
}

// A pack that declares no suites has none to offer, which is different from failing to read it.
func TestAPackWithNoSuitesDirectoryContributesNothing(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/graph-extraction/test", "advanced-security/abom-actions-graph-extraction-test", "")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing from a pack that declares no suites", got)
	}
}

// Suites now live in several packs, so an interface offering only one pack's would silently hide
// the findings lane. Every analysis pack contributes; the CI-only pack does not, which
// TestTheSelfCheckPackIsNotOfferedAsABinding covers.
func TestSuitesFromEveryAnalysisPackAreOffered(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/graph-extraction/src", "advanced-security/abom-actions-graph-extraction-src",
		"codeql-suites", "abom-dependency-graph.qls")
	pack(t, root, "actions/findings/src", "advanced-security/abom-actions-findings-src",
		"codeql-suites", "abom-findings.qls")
	pack(t, root, "actions/selfcheck/src", "advanced-security/abom-actions-selfcheck-src",
		"codeql-suites", "abom-selfcheck.qls")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want one suite from each analysis pack", got)
	}
}

// A missing root means this build has no suites of its own, which is a fact to report rather
// than a failure: the binding may name suites from an installed pack instead.
func TestAMissingPackRootOffersNothingRatherThanFailing(t *testing.T) {
	got, err := AvailableSuites(filepath.Join(t.TempDir(), "nowhere"))
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// Most queries ABOM attributes findings to come from an installed pack, and the registry writes
// `qlpack.yml` where this repository writes `codeql-pack.yml`. Reading only one name left every
// upstream query with no pack and no version — the provenance that matters most.
func TestAQueryFromAnInstalledPackIsStillAttributed(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "codeql", "actions-queries", "0.6.34")
	if err := os.MkdirAll(filepath.Join(packDir, "Security"), 0o755); err != nil {
		t.Fatalf("creating pack: %v", err)
	}
	manifest := "name: codeql/actions-queries\nversion: 0.6.34\n"
	if err := os.WriteFile(filepath.Join(packDir, "qlpack.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	query := filepath.Join(packDir, "Security", "UnpinnedTag.ql")
	if err := os.WriteFile(query, []byte("select 1\n"), 0o600); err != nil {
		t.Fatalf("writing query: %v", err)
	}

	name, version, dir := PackOf(query)

	if name != "codeql/actions-queries" || version != "0.6.34" {
		t.Errorf("PackOf = %q@%q, want the installed pack and its version", name, version)
	}
	// The directory is what gets hashed, so a name without one would leave the query unidentified.
	if dir != packDir {
		t.Errorf("PackOf dir = %q, want %q", dir, packDir)
	}
}

// A query outside any pack has no provenance to report, which the caller records as blank rather
// than failing the round over.
func TestAQueryOutsideAnyPackReportsNoProvenance(t *testing.T) {
	name, version, dir := PackOf(filepath.Join(t.TempDir(), "Loose.ql"))
	if name != "" || version != "" || dir != "" {
		t.Errorf("PackOf = %q@%q in %q, want nothing", name, version, dir)
	}
}
