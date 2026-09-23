package customql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deployed(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(DeploySuitePath)))
	if err != nil {
		t.Fatalf("reading the deployed suite: %v", err)
	}
	return string(body)
}

// Deploying is what moves a query from the space an operator is working in to the set a tier
// runs. The suite names what was deployed rather than selecting the pack, or saving a file would
// be enough to put a query in front of the whole corpus.
func TestTheDeployedSuiteNamesOnlyWhatWasDeployed(t *testing.T) {
	dir := t.TempDir()

	if err := WriteDeployed(dir, []string{"acme/one", "acme/two"}); err != nil {
		t.Fatalf("WriteDeployed: %v", err)
	}

	body := deployed(t, dir)
	for _, want := range []string{"acme/one", "acme/two", "include"} {
		if !strings.Contains(body, want) {
			t.Errorf("the deployed suite lacks %q:\n%s", want, body)
		}
	}
}

// A projection, not a log: taking a query back out has to remove it, and a file that only ever
// grew would keep running something somebody withdrew.
func TestWithdrawingAQueryRemovesItFromTheSuite(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDeployed(dir, []string{"acme/one", "acme/two"}); err != nil {
		t.Fatal(err)
	}

	if err := WriteDeployed(dir, []string{"acme/one"}); err != nil {
		t.Fatal(err)
	}

	body := deployed(t, dir)
	if strings.Contains(body, "acme/two") {
		t.Errorf("a withdrawn query is still in the suite:\n%s", body)
	}
	if !strings.Contains(body, "acme/one") {
		t.Errorf("the query that is still deployed was removed:\n%s", body)
	}
}

// Nothing deployed means nothing runs. A suite with no selection would run the whole pack, which
// is the opposite of what an empty deploy set means, and it would do it across the corpus.
func TestAnEmptyDeploySetRunsNothingRatherThanEverything(t *testing.T) {
	dir := t.TempDir()

	if err := WriteDeployed(dir, nil); err != nil {
		t.Fatalf("WriteDeployed: %v", err)
	}

	body := deployed(t, dir)
	if !strings.Contains(body, "exclude") {
		t.Errorf("an empty deploy set does not exclude anything:\n%s", body)
	}
	if strings.Contains(body, "include") {
		t.Errorf("an empty deploy set names something to include:\n%s", body)
	}
}

// The file is kept rather than removed, so a tier bound to it keeps its binding and simply has
// nothing to run. Removing it would make the binding look invented the next time it was drawn.
func TestAnEmptyDeploySetKeepsTheSuiteFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDeployed(dir, []string{"acme/one"}); err != nil {
		t.Fatal(err)
	}

	if err := WriteDeployed(dir, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(DeploySuitePath))); err != nil {
		t.Errorf("the suite file was removed: %v", err)
	}
}

// Byte-identical for the same set, whatever order it arrives in, so a rewrite that changed
// nothing stays out of the operator's diff.
func TestTheSameSetWritesTheSameFile(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if err := WriteDeployed(first, []string{"acme/two", "acme/one", "acme/two"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteDeployed(second, []string{"acme/one", "acme/two"}); err != nil {
		t.Fatal(err)
	}

	if deployed(t, first) != deployed(t, second) {
		t.Errorf("one set wrote two files:\n%s\n---\n%s",
			deployed(t, first), deployed(t, second))
	}
}

// The two suites are different things: one is what an operator may run on demand, the other is
// what a tier may be bound to. One file serving both would make saving a query a crawl.
func TestTheDeployedSuiteIsNotTheDevelopmentSuite(t *testing.T) {
	if DeploySuite == Suite {
		t.Error("the deployed suite and the development suite are the same reference")
	}
}
