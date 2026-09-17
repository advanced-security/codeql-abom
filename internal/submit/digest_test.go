package submit

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func closureOf(name, ref string) []store.ClosureEntry {
	owner, repo, _ := splitName(name)
	return []store.ClosureEntry{
		{Owner: owner, Repo: repo, Ref: ref, Relationship: "indirect", ParentKey: "vendor/action"},
	}
}

func splitName(name string) (owner, repo, sub string) {
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			return name[:i], name[i+1:], ""
		}
	}
	return name, "", ""
}

// Renewal compares what a repository's supply chain is now against what was last sent. The
// comparison has to be over content: the ledger originally recorded only how many packages were
// submitted, and a version bump keeps that count identical while changing every consumer's answer.
func TestADigestChangesWhenAVersionChanges(t *testing.T) {
	before := Build(meta(), closureOf("actions/cache", "v6.1.0"))
	after := Build(meta(), closureOf("actions/cache", "v6.2.0"))

	if Digest(before) == Digest(after) {
		t.Errorf("a version bump left the digest unchanged (%s); "+
			"the submission would silently keep reporting the old one", Digest(before))
	}
}

// Two submissions of the same supply chain must compare equal, or every check reports a change and
// renewal resubmits the corpus on every pass.
func TestADigestIsStableAcrossRunsOfTheSameClosure(t *testing.T) {
	a := Build(meta(), closureOf("actions/cache", "v6.1.0"))
	m := meta()
	m.SHA = "0000000000000000000000000000000000000000"
	m.ScannedAt = "2026-12-25T00:00:00Z"
	b := Build(m, closureOf("actions/cache", "v6.1.0"))

	if Digest(a) != Digest(b) {
		t.Errorf("digest moved with the commit or the clock: %s then %s", Digest(a), Digest(b))
	}
}

// The closure arrives in whatever order SQL returned it. Order is not content, and treating it as
// content would report a change on every run.
func TestADigestIgnoresTheOrderPackagesArriveIn(t *testing.T) {
	forward := Build(meta(), append(closureOf("actions/cache", "v6.1.0"),
		closureOf("actions/upload-artifact", "v7")...))
	reverse := Build(meta(), append(closureOf("actions/upload-artifact", "v7"),
		closureOf("actions/cache", "v6.1.0")...))

	if Digest(forward) != Digest(reverse) {
		t.Errorf("digest depends on ordering: %s then %s", Digest(forward), Digest(reverse))
	}
}

// A release is a submission of nothing, and it has to be distinguishable from a repository that
// has never been submitted to. Both would otherwise digest to the same empty value, so a
// repository whose transitive dependencies disappeared would never be released.
func TestAReleaseHasItsOwnDigestRatherThanTheEmptyOne(t *testing.T) {
	release := Digest(Build(meta(), nil))
	if release == "" {
		t.Error("a release digests to the empty string, which cannot be told from never submitted")
	}
	if release == Digest(Build(meta(), closureOf("actions/cache", "v6.1.0"))) {
		t.Error("a release digests the same as a submission with content")
	}
}
