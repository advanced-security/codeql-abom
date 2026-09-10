package normalize

import "testing"

// Every expectation here was read out of a GitHub SBOM export, not derived. A purl ABOM invents
// does not merge with the package GitHub already holds — it arrives beside it as a duplicate that
// nothing consumes, behind a submission that still returns 201.
func TestPackageURLMatchesWhatGitHubEmits(t *testing.T) {
	// Observed 2026-09-08 in the exports of advanced-security/dismiss-alerts,
	// codeql-abom-test1 and codeql-abom-test2.
	cases := []struct {
		name                   string
		owner, repo, path, ref string
		want                   string
	}{
		{
			name:  "a major version tag becomes a wildcard range",
			owner: "actions", repo: "checkout", ref: "v4",
			want: "pkg:githubactions/actions/checkout@4.%2A.%2A",
		},
		{
			// codeql-abom-test1 pins `actions/setup-node@v7.0.0`.
			name:  "a full semantic version is exact",
			owner: "actions", repo: "setup-node", ref: "v7.0.0",
			want: "pkg:githubactions/actions/setup-node@7.0.0",
		},
		{
			// codeql-abom-test1 uses `actions/checkout@v7`.
			name:  "a major version tag with no minor or patch published",
			owner: "actions", repo: "checkout", ref: "v7",
			want: "pkg:githubactions/actions/checkout@7.%2A.%2A",
		},
		{
			name:  "a commit sha is passed through untouched",
			owner: "actions", repo: "checkout", ref: "de0fac2e4500dabe0009e67214ff5f5447ce83dd",
			want: "pkg:githubactions/actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd",
		},
		{
			// codeql-abom-test2 pins `actions/checkout` by sha.
			name:  "a sha pin in a second repository",
			owner: "actions", repo: "checkout", ref: "3d3c42e5aac5ba805825da76410c181273ba90b1",
			want: "pkg:githubactions/actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		},
		{
			name:  "a subpath action keeps its path in the name",
			owner: "github", repo: "codeql-action", path: "analyze", ref: "v4",
			want: "pkg:githubactions/github/codeql-action/analyze@4.%2A.%2A",
		},
		{
			name:  "a subpath action pinned by sha",
			owner: "github", repo: "codeql-action", path: "init",
			ref:  "8aad20d150bbac5944a9f9d289da16a4b0d87c1e",
			want: "pkg:githubactions/github/codeql-action/init@8aad20d150bbac5944a9f9d289da16a4b0d87c1e",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PackageURL(c.owner, c.repo, c.path, c.ref); got != c.want {
				t.Errorf("PackageURL = %q, want %q", got, c.want)
			}
		})
	}
}

// The rule behind the observations: strip the `v`, then pad the missing semantic version
// components with wildcards. `vN.M` is not published as a tag by any action in the corpus, so it
// is covered by the rule rather than by a measurement — which is why it is stated here.
func TestAMajorMinorReferencePadsOnlyThePatch(t *testing.T) {
	if got := PackageURL("acme", "widget", "", "v2.3"); got != "pkg:githubactions/acme/widget@2.3.%2A" {
		t.Errorf("PackageURL = %q, want the patch wildcarded", got)
	}
}

// GitHub emits no package for a local action, so emitting one would put a dependency in the graph
// that GitHub will never reconcile against anything. codeql-abom-test1 uses
// `./.github/actions/build-app` and GitHub's export contains no package for it.
//
// Owner and repo are populated deliberately: a local action resolves to a real repository, so the
// reference is what must suppress the package. An earlier version of this test left them empty and
// passed because of that, never reaching the branch it claimed to cover.
func TestALocalActionHasNoPackageURL(t *testing.T) {
	for _, ref := range []string{"", "./", "./.github/actions/build-app"} {
		if got := PackageURL("advanced-security", "codeql-abom-test1", "", ref); got != "" {
			t.Errorf("PackageURL(ref=%q) = %q, want empty", ref, got)
		}
	}
}

// A container action is the other class GitHub does not represent. codeql-abom-test2 uses
// `docker://alpine:3.20` and GitHub's export contains no package for it. It is a real dependency
// and it is not a `githubactions` package; claiming otherwise would invent an action.
func TestAContainerActionHasNoGitHubActionsPackageURL(t *testing.T) {
	if got := PackageURL("library", "alpine", "", "docker://alpine:3.20"); got != "" {
		t.Errorf("PackageURL = %q, want empty for a container action", got)
	}
}

// The repository itself is not a `githubactions` package. GitHub describes it with a different
// purl type and versions it by branch — `pkg:github/advanced-security/codeql-abom-test1@main` —
// so a self-reference must not be emitted as an action.
func TestTheRepositoryItselfIsNotAnActionsPackage(t *testing.T) {
	if got := PackageURL("advanced-security", "dismiss-alerts", "", ""); got != "" {
		t.Errorf("PackageURL = %q, want empty for a repository self-reference", got)
	}
}

// An owner or name we do not have is not a package. Emitting `pkg:githubactions//@v1` would be a
// dependency on an account with no name, which is the fabricated-verdict class the first
// architectural invariant refuses.
func TestAnIncompleteReferenceHasNoPackageURL(t *testing.T) {
	cases := [][4]string{
		{"", "checkout", "", "v4"},
		{"actions", "", "", "v4"},
		{"actions", "checkout", "", ""},
		{"${{ github.repository }}", "setup", "", "v1"},
	}
	for _, c := range cases {
		if got := PackageURL(c[0], c[1], c[2], c[3]); got != "" {
			t.Errorf("PackageURL(%q,%q,%q,%q) = %q, want empty", c[0], c[1], c[2], c[3], got)
		}
	}
}

// The version is the only component GitHub percent-encodes, and it encodes `*` as `%2A`. Encoding
// the name too would produce a package that never matches.
func TestOnlyTheVersionIsEncoded(t *testing.T) {
	got := PackageURL("actions", "checkout", "", "v4")
	if want := "pkg:githubactions/actions/checkout@"; got[:len(want)] != want {
		t.Errorf("name was encoded: %q", got)
	}
}
