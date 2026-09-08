package bomexport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A document nobody else can read is not an export. These check the shape other tooling relies
// on, not that the library round-trips its own types.
func TestCycloneDXNamesEveryComponentAndItsDependencies(t *testing.T) {
	out, err := CycloneDX(sample())
	if err != nil {
		t.Fatalf("CycloneDX: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("the writer produced something that is not JSON: %v", err)
	}

	if doc["bomFormat"] != "CycloneDX" {
		t.Errorf("bomFormat = %v, want CycloneDX", doc["bomFormat"])
	}
	if doc["specVersion"] != "1.6" {
		t.Errorf("specVersion = %v, want 1.6", doc["specVersion"])
	}
	components, _ := doc["components"].([]any)
	if len(components) == 0 {
		t.Fatal("no components reached the CycloneDX document")
	}
	if _, ok := doc["dependencies"]; !ok {
		t.Error("a bill of materials with no dependency graph is a list, not a BOM")
	}
}

// The reference as written is the most useful thing the document carries, and a format that
// cannot hold it is a format that loses the evidence.
func TestCycloneDXCarriesTheReferenceAsWritten(t *testing.T) {
	out, err := CycloneDX(sample())
	if err != nil {
		t.Fatalf("CycloneDX: %v", err)
	}
	if !strings.Contains(string(out), "actions/checkout@v4") {
		t.Error("the reference as written did not survive into the CycloneDX document")
	}
}

// An unanalyzed component is in the document and marked. Omitting it would make the export look
// complete, which is the failure the coverage statement exists to prevent.
//
// Checked against the parsed `components` array rather than by substring: the name also appears
// in `dependencies` and in a relationship, so a substring search stayed green against a writer
// that dropped every unanalyzed component.
func TestCycloneDXKeepsUnanalyzedComponentsAndSaysSo(t *testing.T) {
	out, err := CycloneDX(sample())
	if err != nil {
		t.Fatalf("CycloneDX: %v", err)
	}
	var doc struct {
		Components []struct {
			Name       string `json:"name"`
			Properties []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"properties"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parsing the export: %v", err)
	}

	var found bool
	for _, c := range doc.Components {
		if c.Name != "actions/setup-go" {
			continue
		}
		found = true
		var marked bool
		for _, p := range c.Properties {
			if p.Name == "abom:analyzed" {
				marked = true
				if p.Value != "false" {
					t.Errorf("abom:analyzed = %q for a component nothing examined", p.Value)
				}
			}
		}
		if !marked {
			t.Error("the component says nothing about whether it was analyzed")
		}
	}
	if !found {
		t.Error("the unanalyzed component was dropped from the components array")
	}
}

func TestSPDXProducesA23DocumentWithEveryComponent(t *testing.T) {
	out, err := SPDX(sample())
	if err != nil {
		t.Fatalf("SPDX: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("the writer produced something that is not JSON: %v", err)
	}

	if doc["spdxVersion"] != "SPDX-2.3" {
		t.Errorf("spdxVersion = %v, want SPDX-2.3", doc["spdxVersion"])
	}
	packages, _ := doc["packages"].([]any)
	if len(packages) == 0 {
		t.Fatal("no packages reached the SPDX document")
	}
	if _, ok := doc["relationships"]; !ok {
		t.Error("SPDX without relationships states no dependencies at all")
	}
}

// SPDX has no field for "we did not look at this", so it goes in a comment rather than being
// silently dropped. A format's limits are not a license to omit the measurement.
func TestSPDXStatesCoverageRatherThanImplyingCompleteness(t *testing.T) {
	out, err := SPDX(sample())
	if err != nil {
		t.Fatalf("SPDX: %v", err)
	}
	if !strings.Contains(string(out), "analyzed") {
		t.Error("the SPDX document does not state what was examined")
	}
}

// SPDX element ids allow only letters, digits, `.` and `-`, so two different components can
// reduce to one id. `actions/upload-artifact/merge` and `actions/upload-artifact-merge` did
// exactly that on the reference corpus. Two packages sharing an id makes the document invalid,
// and silently merging two dependencies into one is worse than that.
func TestTwoComponentsNeverShareAnSPDXIdentifier(t *testing.T) {
	doc := store.BOM{
		Roots:       []string{"octo-org/app"},
		GeneratedAt: "2026-09-07T00:00:00Z",
		Components: []store.BOMComponent{
			{Key: "actions/upload-artifact/merge", Kind: "action"},
			{Key: "actions/upload-artifact-merge", Kind: "action"},
			// Made entirely of punctuation: an unexpanded `${{ }}` reaches the graph as this.
			{Key: "$/", Kind: "action"},
		},
	}
	out, err := SPDX(doc)
	if err != nil {
		t.Fatalf("SPDX: %v", err)
	}

	var parsed struct {
		Packages []struct {
			Name string `json:"name"`
			ID   string `json:"SPDXID"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(parsed.Packages) != 3 {
		t.Fatalf("packages = %d, want 3", len(parsed.Packages))
	}
	seen := map[string]string{}
	for _, p := range parsed.Packages {
		if p.ID == "SPDXRef-" || p.ID == "" {
			t.Errorf("%s got an empty identifier", p.Name)
		}
		if other, clash := seen[p.ID]; clash {
			t.Errorf("%s and %s share the identifier %q", other, p.Name, p.ID)
		}
		seen[p.ID] = p.Name
	}
}

// 44 roots produced a 1,400-character document name and namespace, which no reader can use.
func TestTheDocumentNameStaysReadableWithManyRoots(t *testing.T) {
	roots := make([]string, 0, 44)
	for i := range 44 {
		roots = append(roots, fmt.Sprintf("facebook/repo-%d", i))
	}
	out, err := SPDX(store.BOM{Roots: roots, GeneratedAt: "2026-09-07T00:00:00Z"})
	if err != nil {
		t.Fatalf("SPDX: %v", err)
	}
	var parsed struct {
		Name      string `json:"name"`
		Namespace string `json:"documentNamespace"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(parsed.Name) > 80 {
		t.Errorf("name is %d characters: %q", len(parsed.Name), parsed.Name)
	}
	if !strings.Contains(parsed.Name, "43-more") {
		t.Errorf("name = %q, want it to say how many roots it stands for", parsed.Name)
	}
	// The roots themselves are still stated, in the comment where there is room for them.
	if len(parsed.Namespace) > 120 {
		t.Errorf("namespace is %d characters", len(parsed.Namespace))
	}
}

// Both writers must be reproducible for the same reason the native document is: a reader
// regenerates and compares.
func TestBothWritersAreDeterministic(t *testing.T) {
	for _, w := range []struct {
		name  string
		write func(store.BOM) ([]byte, error)
	}{
		{"CycloneDX", CycloneDX},
		{"SPDX", SPDX},
	} {
		first, err := w.write(sample())
		if err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		second, err := w.write(sample())
		if err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if string(first) != string(second) {
			t.Errorf("%s produced two different documents for one input", w.name)
		}
	}
}

// A document with no components is a real answer for a scope that depends on nothing, and the
// writers must not fall over on it.
func TestAnEmptyDocumentStillSerializes(t *testing.T) {
	empty := store.BOM{Roots: []string{"octo-org/app"}, GeneratedAt: "2026-09-07T00:00:00Z"}
	if _, err := CycloneDX(empty); err != nil {
		t.Errorf("CycloneDX on an empty document: %v", err)
	}
	if _, err := SPDX(empty); err != nil {
		t.Errorf("SPDX on an empty document: %v", err)
	}
}

// sample is a document with one analyzed component, one unanalyzed, and one finding.
func sample() store.BOM {
	return store.BOM{
		Roots:       []string{"octo-org/app"},
		GeneratedAt: "2026-09-07T00:00:00Z",
		Components: []store.BOMComponent{
			{
				Key: "actions/checkout", Kind: "action", Repository: "actions/checkout",
				Direct: true, Depth: 1, Analyzed: true, RefsInUse: 1, RefsAnalyzed: 1,
				References: []store.BOMReference{
					{Raw: "actions/checkout@v4", Value: "v4", ResolvedSHA: "cafe1234",
						Analyzed: true},
				},
				Findings: []store.BOMFinding{
					{RuleID: "actions/unpinned-tag", Repository: "octo-org/app",
						FilePath: ".github/workflows/ci.yml", StartLine: 1,
						Severity: "warning", RunKey: "run-1", PackDigest: "sha256:pack",
						ResultHash: "sha256:result"},
				},
				Advisories: []store.BOMAdvisory{
					{GHSAID: "GHSA-xxxx-yyyy-zzzz", Severity: "high", Summary: "example",
						VulnerableRange: "< 4.0.0"},
				},
			},
			{
				Key: "actions/setup-go", Kind: "action", Repository: "actions/setup-go",
				Depth: 2, Analyzed: false, RefsInUse: 1, RefsAnalyzed: 0,
				References: []store.BOMReference{
					{Raw: "actions/setup-go@v5", Value: "v5"},
				},
			},
		},
		Relationships: []store.BOMRelationship{
			{Source: "actions/checkout", Target: "actions/setup-go",
				Kind: "step-uses-action", Boundary: store.BoundarySameOwner},
		},
		Coverage: store.BOMCoverage{
			ComponentsInUse: 2, ComponentsAnalyzed: 1,
			ReferencesInUse: 2, ReferencesAnalyzed: 1,
			Findings: 1, FindingsUntraced: 0,
		},
	}
}
