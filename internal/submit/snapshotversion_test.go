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

// A dependency a lock selects is submitted as what runs: the locked commit, named by the version
// the crawl measured it as when it has one. The tag the workflow wrote names whatever that tag
// points at today, which a locked consumer does not run.
func TestALockedDependencyIsSubmittedAsTheCommitItsLockSelects(t *testing.T) {
	const locked = "11bd71901bbe5b1630ceea73d27597364c9af683"
	for name, c := range map[string]struct {
		entry store.ClosureEntry
		want  string
	}{
		"measured": {store.ClosureEntry{Owner: "actions", Repo: "checkout", Relationship: "indirect",
			Ref: "v4", LockedCommit: locked, Version: "v4.2.2"},
			"pkg:githubactions/actions/checkout@4.2.2"},
		"unmeasured": {store.ClosureEntry{Owner: "actions", Repo: "checkout", Relationship: "indirect",
			Ref: "v4", LockedCommit: locked},
			"pkg:githubactions/actions/checkout@" + locked},
	} {
		resolved := Build(meta(), []store.ClosureEntry{c.entry}).Manifests[SyntheticManifestPath].Resolved
		if _, ok := resolved[c.want]; !ok || len(resolved) != 1 {
			t.Errorf("%s: resolved = %v, want %s alone", name, keys(resolved), c.want)
		}
	}
}
