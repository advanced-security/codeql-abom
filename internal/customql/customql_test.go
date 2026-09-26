package customql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The operator's queries live on disk as a pack, which is what lets the same pack be tested,
// versioned and published without ABOM. A directory that is not a pack resolves to nothing.
func TestScaffoldingWritesAPackTheCLICanResolve(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")

	wrote, err := Scaffold(dir, "2.27.0")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if !wrote {
		t.Error("scaffolding an empty directory wrote nothing")
	}

	for _, want := range []string{"qlpack.yml", SuitePath, "src", "test", "test/qlpack.yml"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("the pack has no %s: %v", want, err)
		}
	}
}

// `codeql test run` refuses a test pack that names no extractor, and the failure reads as a
// broken query rather than a missing line. Scaffolding it is the difference between a directory
// an operator can write a test in and one they have to debug first.
func TestTheTestPackDeclaresItsExtractor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "test", "qlpack.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "extractor: actions") {
		t.Errorf("the test pack declares no extractor:\n%s", body)
	}
	if !strings.Contains(string(body), Pack) {
		t.Errorf("the test pack does not depend on the queries it tests:\n%s", body)
	}
}

// Two packs, because a test pack declares an extractor and a query pack must not: one manifest
// cannot be both, and the CLI reads them differently.
func TestTheTestsAreTheirOwnPack(t *testing.T) {
	if TestPack == Pack {
		t.Fatal("the tests share the query pack's name, which the CLI refuses")
	}
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	queries, err := os.ReadFile(filepath.Join(dir, "qlpack.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(queries), "extractor:") {
		t.Errorf("the query pack declares an extractor:\n%s", queries)
	}
}

// The pack is pinned to the CodeQL the rest of ABOM runs. A query unit-tested against one version
// of the actions library and executed under another can behave differently, and nothing
// downstream would say which one produced a result.
func TestThePackIsPinnedToTheRunningCodeQL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "qlpack.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "version: 2.27.0") {
		t.Errorf("the pack does not record what it was written for:\n%s", body)
	}
}

// One actions library governs writing a query and running it: a wildcard resolves to whatever the
// package cache holds when the lock is written, which is how the operator's pack came to lock
// 0.6.2 while ABOM's own queries ran 0.6.1.
func TestBothPacksPinTheActionsLibraryABOMsOwnQueriesRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.1"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	lib, err := os.ReadFile(filepath.Join("..", "..", "ql", "actions", "lib", "codeql-pack.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lib), "codeql/actions-all: "+ActionsAll+"\n") {
		t.Fatalf("ABOM's library pack does not pin codeql/actions-all %s:\n%s", ActionsAll, lib)
	}
	for _, manifest := range []string{"qlpack.yml", "test/qlpack.yml"} {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifest)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "codeql/actions-all: "+ActionsAll+"\n") {
			t.Errorf("%s does not pin codeql/actions-all %s:\n%s", manifest, ActionsAll, body)
		}
	}
}

// A marker in a fixture is read only by the postprocess query, so a test pack without it has
// no way to fail before its query exists except for want of an .expected.
func TestTheTestPackCarriesTheQueryThatReadsItsMarkers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.1"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "test", filepath.FromSlash(Postprocess)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@kind test-postprocess", "InlineExpectationsTest", "YamlComment"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the postprocess query does not say %q:\n%s", want, body)
		}
	}
}

// The operator's own work lives here. A scaffold that rewrote the manifest would silently undo a
// dependency they added, and it runs on every start.
func TestScaffoldingNeverOverwritesWhatIsAlreadyThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	manifest := filepath.Join(dir, "qlpack.yml")
	edited := "name: " + Pack + "\nversion: 9.9.9\nlibrary: false\n"
	if err := os.WriteFile(manifest, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := Scaffold(dir, "2.27.0")
	if err != nil {
		t.Fatalf("Scaffold (repeat): %v", err)
	}
	if wrote {
		t.Error("a second scaffold reported writing, so it is not idempotent")
	}

	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != edited {
		t.Errorf("the operator's manifest was overwritten:\n%s", body)
	}
}

// The suite selects the pack rather than listing queries, so adding a `.ql` is enough. A suite
// naming queries one by one is a second place to keep in step, and the one that falls behind
// silently stops running something the operator wrote.
func TestTheSuiteSelectsThePackRatherThanListingQueries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(SuitePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "queries: .") {
		t.Errorf("the suite does not select the whole pack:\n%s", body)
	}
	if strings.Contains(string(body), ".ql\n") {
		t.Errorf("the suite names queries individually:\n%s", body)
	}
}

// The CLI refuses a pack name found twice on one search path, and both directories are on it.
func TestThePackIsNamedApartFromThisRepositorysOwn(t *testing.T) {
	if strings.HasPrefix(Pack, "advanced-security/abom-") {
		t.Errorf("%q collides with the naming this repository's own packs use", Pack)
	}
}

// A directory nobody named is a refusal rather than a scaffold in the working directory.
func TestScaffoldingRefusesAnUnnamedDirectory(t *testing.T) {
	if _, err := Scaffold("", "2.27.0"); err == nil {
		t.Error("scaffolding with no directory was accepted")
	}
}
