package submit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func meta() Metadata {
	return Metadata{
		Repository: "advanced-security/codeql-abom-test1",
		SHA:        "5f2a2d7425ce7567773b0d95894d961c8c659f07",
		Ref:        "refs/heads/main",
		ScannedAt:  "2026-09-08T22:00:00Z",
	}
}

// Owning a path GitHub analyzes replaces GitHub's reading of it, so the manifest path is the
// safety property and nothing may point it at a workflow.
func TestTheManifestNeverClaimsAPathGitHubAnalyzes(t *testing.T) {
	closure := []store.ClosureEntry{
		{Manifest: ".github/workflows/ci.yml", Owner: "helper", Repo: "tool", Ref: "v2",
			Relationship: "indirect", ParentKey: "vendor/action"},
	}
	snap := Build(meta(), closure)

	for name, m := range snap.Manifests {
		if strings.HasPrefix(m.File.SourceLocation, ".github/workflows/") {
			t.Fatalf("manifest %q claims a workflow path: %s", name, m.File.SourceLocation)
		}
		if m.File.SourceLocation != SyntheticManifestPath {
			t.Errorf("manifest %q source_location = %q, want the synthetic path",
				name, m.File.SourceLocation)
		}
	}
}

// GitHub already finds direct dependencies by static analysis. Resubmitting them adds nothing and
// would make the submission look like a replacement for work GitHub is doing correctly.
func TestOnlyTheDependenciesGitHubCannotSeeAreSubmitted(t *testing.T) {
	closure := []store.ClosureEntry{
		{Owner: "actions", Repo: "checkout", Ref: "v4", Relationship: "direct"},
		{Owner: "helper", Repo: "tool", Ref: "v2", Relationship: "indirect",
			ParentKey: "vendor/action"},
	}
	snap := Build(meta(), closure)

	var got []string
	for _, m := range snap.Manifests {
		for _, r := range m.Resolved {
			got = append(got, r.PackageURL)
		}
	}
	if len(got) != 1 || got[0] != "pkg:githubactions/helper/tool@2.%2A.%2A" {
		t.Errorf("resolved = %v, want only the indirect dependency", got)
	}
}

// A reference with no package \u2014 a local action, a container action, an unresolved expression \u2014 must
// not become a package with an empty version, which is the fabricated-dependency class the first
// architectural invariant refuses.
func TestAReferenceWithNoPackageIsDropped(t *testing.T) {
	closure := []store.ClosureEntry{
		{Owner: "acme", Repo: "widget", Ref: "docker://alpine:3.20", Relationship: "indirect"},
		{Owner: "", Repo: "", Ref: "./local", Relationship: "indirect"},
	}
	snap := Build(meta(), closure)

	for _, m := range snap.Manifests {
		if len(m.Resolved) != 0 {
			t.Errorf("resolved = %+v, want nothing submittable", m.Resolved)
		}
	}
}

// Nothing to add must produce a snapshot with no manifests rather than no submission at all.
// An empty manifests object releases a previously submitted path back to GitHub's static analysis.
func TestAnEmptyClosureReleasesThePathInsteadOfHoldingItBlank(t *testing.T) {
	snap := Build(meta(), nil)

	if snap.Manifests == nil {
		t.Fatal("manifests is nil; it must serialize as {} so the submission releases the path")
	}
	if len(snap.Manifests) != 0 {
		t.Errorf("manifests = %+v, want empty", snap.Manifests)
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"manifests":{}`) {
		t.Errorf("serialized snapshot does not carry an empty manifests object: %s", body)
	}
}

// The correlator and detector name are the identity GitHub supersedes on. If they drift between
// runs the old submission is never replaced and the repository accumulates stale manifests that
// nothing maintains.
func TestTheCorrelatorIsStableAcrossRuns(t *testing.T) {
	a := Build(meta(), nil)
	m := meta()
	m.SHA = "0000000000000000000000000000000000000000"
	m.ScannedAt = "2026-12-25T00:00:00Z"
	b := Build(m, nil)

	if a.Job.Correlator != b.Job.Correlator {
		t.Errorf("correlator drifted: %q then %q", a.Job.Correlator, b.Job.Correlator)
	}
	if a.Detector.Name != b.Detector.Name {
		t.Errorf("detector drifted: %q then %q", a.Detector.Name, b.Detector.Name)
	}
}

// A dependency is submitted with the parent it was reached through, which is what lets Dependabot
// open a pull request against the parent rather than against a package the repository cannot bump.
func TestATransitiveDependencyRecordsTheParentItWasReachedThrough(t *testing.T) {
	closure := []store.ClosureEntry{
		{Owner: "vendor", Repo: "action", Ref: "v1", Relationship: "direct"},
		{Owner: "helper", Repo: "tool", Ref: "v2", Relationship: "indirect",
			ParentKey: "vendor/action"},
	}
	snap := Build(meta(), closure)

	for _, m := range snap.Manifests {
		for key, r := range m.Resolved {
			if r.Relationship != "indirect" {
				t.Errorf("%s relationship = %q, want indirect", key, r.Relationship)
			}
			if r.Scope != "runtime" {
				t.Errorf("%s scope = %q, want runtime", key, r.Scope)
			}
		}
	}
}
