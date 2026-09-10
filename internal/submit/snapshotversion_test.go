package submit

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// GitHub generates Dependabot alerts for actions "that use semantic versioning, not SHA
// versioning", so a commit-pinned dependency submitted as its commit can never be matched against
// an advisory. Where the crawl watched a tag resolve to that commit, the version is known and the
// submission should say it.
func TestAResolvedVersionIsSubmittedInsteadOfTheCommit(t *testing.T) {
	snap := Build(meta(), []store.ClosureEntry{{
		Owner: "actions", Repo: "checkout", Relationship: "indirect",
		Ref:     "de0fac2e4500dabe0009e67214ff5f5447ce83dd",
		Version: "v4.2.2",
	}})

	resolved := snap.Manifests[SyntheticManifestPath].Resolved
	if _, ok := resolved["pkg:githubactions/actions/checkout@4.2.2"]; !ok {
		t.Errorf("resolved = %v, want the version the commit was released as", keys(resolved))
	}
}

// And with nothing measured, the commit is still what is submitted. A version nobody verified
// would put a dependency the repository does not have in front of Dependabot.
func TestAnUnresolvedCommitIsStillSubmittedAsTheCommit(t *testing.T) {
	snap := Build(meta(), []store.ClosureEntry{{
		Owner: "actions", Repo: "checkout", Relationship: "indirect",
		Ref: "de0fac2e4500dabe0009e67214ff5f5447ce83dd",
	}})

	resolved := snap.Manifests[SyntheticManifestPath].Resolved
	if _, ok := resolved["pkg:githubactions/actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd"]; !ok {
		t.Errorf("resolved = %v, want the commit when no version was measured", keys(resolved))
	}
}

// A tag reference needs no substitution, and must not gain one: what the workflow says is already
// the version.
func TestATagReferenceIsUnaffected(t *testing.T) {
	snap := Build(meta(), []store.ClosureEntry{{
		Owner: "actions", Repo: "checkout", Relationship: "indirect", Ref: "v4",
	}})

	resolved := snap.Manifests[SyntheticManifestPath].Resolved
	if _, ok := resolved["pkg:githubactions/actions/checkout@4.%2A.%2A"]; !ok {
		t.Errorf("resolved = %v, want the major-version range unchanged", keys(resolved))
	}
}

func keys(m map[string]Resolved) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
