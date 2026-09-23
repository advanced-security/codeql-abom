package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Naming a directory is the whole opt-in. An operator who has one gets it created rather than
// being told to construct a CodeQL pack by hand before writing their first query.
func TestNamingADirectoryScaffoldsThePackAndPutsItOnTheSearchPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	defer runner.SetCustomPackRoot("")

	var out bytes.Buffer
	openCustomQueries(dir, "", &out, &out)

	if _, err := os.Stat(filepath.Join(dir, "qlpack.yml")); err != nil {
		t.Errorf("no pack was created: %v", err)
	}
	if runner.CustomPackRoot() != dir {
		t.Errorf("the search path is %q, want %q", runner.CustomPackRoot(), dir)
	}
	if !strings.Contains(out.String(), dir) {
		t.Errorf("nothing said where the pack is: %q", out.String())
	}
}

// ABOM must not create a directory in somebody's working tree because a binary started.
func TestWithNoDirectoryNamedNothingIsCreated(t *testing.T) {
	defer runner.SetCustomPackRoot("")

	var out bytes.Buffer
	openCustomQueries("", "", &out, &out)

	if runner.CustomPackRoot() != "" {
		t.Errorf("a pack directory was configured from nothing: %q", runner.CustomPackRoot())
	}
	if out.Len() != 0 {
		t.Errorf("an unconfigured feature announced itself: %q", out.String())
	}
}

// A run resolves a query id through the suites it can see. Without the operator's, a query they
// wrote is refused as one no suite offers — which reads as the query being wrong.
func TestARunCanNameAQueryTheOperatorWroteOnlyWhenTheirPackIsConfigured(t *testing.T) {
	defer runner.SetCustomPackRoot("")

	runner.SetCustomPackRoot("")
	if slices.Contains(runnableSuites(), customql.Suite) {
		t.Error("the operator's suite is offered with no directory configured")
	}

	runner.SetCustomPackRoot(t.TempDir())
	suites := runnableSuites()
	if !slices.Contains(suites, customql.Suite) {
		t.Errorf("the operator's suite is missing from %v", suites)
	}
	for _, shipped := range store.DefaultTierSuites[store.TierDirect] {
		if !slices.Contains(suites, shipped) {
			t.Errorf("configuring a custom pack dropped the shipped suite %q", shipped)
		}
	}
}

// What a run *can name* and what the crawl runs are separate. A pack that joined the tier
// bindings by existing would make writing a query a way of scheduling work across the corpus.
func TestConfiguringAPackDoesNotBindItToATier(t *testing.T) {
	defer runner.SetCustomPackRoot("")
	runner.SetCustomPackRoot(t.TempDir())

	for tier, suites := range store.DefaultTierSuites {
		if slices.Contains(suites, customql.Suite) {
			t.Errorf("the operator's suite is bound to the %s tier by default", tier)
		}
	}
}
