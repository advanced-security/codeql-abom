package expand

import (
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// configReference is how has-ghas points every repository at its central configuration file.
const configReference = "remote=has-ghas/secureCodeScan-codeql-gha@main:.github/codeql-config.yml"

func setConfigFile(t *testing.T, h *harness, value string) {
	t.Helper()
	if err := h.store.ReplacePropertyValues(codeScanningRepo, []store.PropertyValue{
		{Name: "github-codeql-config-file", Value: value}}); err != nil {
		t.Fatalf("ReplacePropertyValues: %v", err)
	}
}

// A configuration file that narrows paths narrows what default setup extracted, so neither its
// database nor its analysis can answer for every file ABOM reads; both are refused, and counted.
func TestAConfigurationFileThatNarrowsPathsLeavesTheWorkToABOM(t *testing.T) {
	h, cs, a := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
	setConfigFile(t, h, configReference)
	cs.files = map[string]string{
		"has-ghas/secureCodeScan-codeql-gha|.github/codeql-config.yml|main": "name: central\npaths-ignore:\n  - .github/actions/**\n",
	}

	if stats := runBound(t, h); stats.Failed != 0 {
		t.Fatalf("%d repositories failed", stats.Failed)
	}

	got := h.expander.ReuseCounts()
	if got.Databases != 0 || got.Analyses != 0 ||
		got.Refused["database: "+refusedConfigFile] != 1 || got.Refused["analysis: "+refusedConfigFile] != 1 {
		t.Errorf("reuse = %+v, want the database and the analysis both refused: %s", got, refusedConfigFile)
	}
	if cs.downloaded() != 0 || cs.sarifs != 0 {
		t.Errorf("downloaded %d databases and %d analyses that could not be used", cs.downloaded(), cs.sarifs)
	}
	if asked := a.asked(); len(asked) != 1 || !slices.Equal(asked[0], []string{"findings.qls"}) {
		t.Errorf("analyze ran %v, want the whole lane", asked)
	}
}

// has-ghas's file names itself and nothing else, which narrows nothing: reuse goes ahead.
func TestAConfigurationFileThatNarrowsNothingChangesNothing(t *testing.T) {
	h, cs, _ := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
	setConfigFile(t, h, configReference)
	cs.files = map[string]string{
		"has-ghas/secureCodeScan-codeql-gha|.github/codeql-config.yml|main": "name: 'ORG-DEFAULT: central'\n",
	}

	runBound(t, h)

	if got := h.expander.ReuseCounts(); got.Databases != 1 || got.Analyses != 1 || len(got.Refused) != 0 {
		t.Errorf("reuse = %+v, want the database and the analysis taken", got)
	}
}

// A repository that sets no value gets its organization's default, which is the file default setup reads.
func TestTheOrganizationsDefaultIsTheFileARepositoryWithoutAValueGets(t *testing.T) {
	h, cs, _ := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
	if err := h.store.ReplacePropertySchema("has-ghas", []store.PropertyDefinition{
		{Name: "github-codeql-config-file", HasDefault: true, DefaultValue: "remote=central"}}); err != nil {
		t.Fatalf("ReplacePropertySchema: %v", err)
	}
	cs.files = map[string]string{"has-ghas/central|.github/codeql-config.yml|main": "paths:\n  - src\n"}

	runBound(t, h)

	if got := h.expander.ReuseCounts(); got.Databases != 0 || got.Refused["database: "+refusedConfigFile] != 1 {
		t.Errorf("reuse = %+v, want the organization's default read and the database refused", got)
	}
}

// A file that cannot be read cannot be shown to narrow nothing.
func TestAConfigurationFileThatCannotBeReadIsRefused(t *testing.T) {
	h, cs, _ := splitHarness(t, lanePacks(t, t.TempDir(), "0.6.36"))
	setConfigFile(t, h, "remote=elsewhere/private@main:.github/codeql-config.yml")
	cs.fileErr = &catalog.StatusError{Code: 404}

	runBound(t, h)

	if got := h.expander.ReuseCounts(); got.Databases != 0 || got.Analyses != 0 ||
		got.Refused["database: "+refusedConfigUnread] != 1 || got.Refused["analysis: "+refusedConfigUnread] != 1 {
		t.Errorf("reuse = %+v, want both refused: %s", got, refusedConfigUnread)
	}
}

func TestConfigurationReferencesFollowGitHubsDefaults(t *testing.T) {
	for _, c := range []struct{ value, repo, path, ref string }{
		{"remote=central", "has-ghas/central", ".github/codeql-config.yml", "main"},
		{"remote=acme/central@v2:cfg/codeql.yml", "acme/central", "cfg/codeql.yml", "v2"},
		{"remote=central:x.yml", "has-ghas/central", "x.yml", "main"},
		{".github/codeql/local.yml", "has-ghas/test-repo-3", ".github/codeql/local.yml", "abc123"},
	} {
		repo, path, ref := configReferenceOf(c.value, "has-ghas/test-repo-3", "abc123")
		if repo != c.repo || path != c.path || ref != c.ref {
			t.Errorf("%q read as %s %s %s, want %s %s %s", c.value, repo, path, ref, c.repo, c.path, c.ref)
		}
	}
}
