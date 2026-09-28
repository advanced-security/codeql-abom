package lockfile

import (
	"slices"
	"strings"

	native "github.com/github/actions-lockfile/go/pkg/lockfile"
)

// Dependency is one entry of a lockfile's dependency map: what a pin key selects, and the
// identity the lock attributes to it.
//
// Evidence the lock asserts, not evidence anything verified. A structurally valid entry does not
// prove its commit is reachable from its ref, and the numeric ids are GitHub's as the tool
// recorded them, never an ownership declaration.
type Dependency struct {
	// Pin is the canonical key, OWNER/REPO@REF with the owner and repository in lower case.
	Pin   string
	Owner string
	Repo  string
	// Ref is the pin key's ref: what a workflow or composite action asked for.
	Ref string
	// ResolvedFrom is the entry's own ref field, which differs from Ref whenever an entry is
	// keyed by commit and narrowed to the tag it was resolved from.
	ResolvedFrom string
	// Algorithm and Digest split the algorithm-prefixed commit. Both are empty for an entry
	// whose commit is not in that form, which then selects nothing.
	Algorithm string
	Digest    string
	OwnerID   int64
	RepoID    int64
	// Uses are the pin keys of the entry's direct nested dependencies: the lock's DAG.
	Uses []string
}

// WorkflowPin is one pin a workflow entry lists.
type WorkflowPin struct {
	Path string
	Pin  string
}

// Dependencies reports every entry of a lock this consumer can read, ordered by pin.
//
// Nothing for an absent or unsupported lock: an entry of a schema that could not be read is not
// a selection, and reporting one would let the unreadable file choose commits.
func (l Lock) Dependencies() []Dependency {
	if !l.parsed {
		return nil
	}
	out := make([]Dependency, 0, len(l.file.Dependencies))
	for key, action := range l.file.Dependencies {
		pin, ok := native.ParsePin(key)
		if !ok {
			continue
		}
		algorithm, digest := splitCommit(action.Commit)
		out = append(out, Dependency{
			Pin: pin.String(), Owner: pin.Owner, Repo: pin.Repo, Ref: pin.Ref,
			ResolvedFrom: action.Ref, Algorithm: algorithm, Digest: digest,
			OwnerID: action.OwnerID, RepoID: action.RepoID,
			Uses: slices.Clone(action.Uses),
		})
	}
	slices.SortFunc(out, func(a, b Dependency) int { return strings.Compare(a.Pin, b.Pin) })
	return out
}

// WorkflowPins reports the pins each workflow entry lists, ordered by path and then as written.
//
// These are the pins the workflow and its own local composites name. The parser documents the
// list as a flat transitive closure; the pinned CLI does not write it that way, so what a remote
// composite action pulls in is reachable through its entry's Uses and nowhere else.
func (l Lock) WorkflowPins() []WorkflowPin {
	if !l.parsed {
		return nil
	}
	paths := make([]string, 0, len(l.file.Workflows))
	for path := range l.file.Workflows {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	var out []WorkflowPin
	for _, path := range paths {
		for _, key := range l.file.Workflows[path] {
			out = append(out, WorkflowPin{Path: path, Pin: key})
		}
	}
	return out
}

// splitCommit separates the algorithm prefix the lockfile records a commit with. A commit in any
// other form, or a digest of the wrong length or alphabet, names nothing runnable.
func splitCommit(commit string) (algorithm, digest string) {
	name, value, ok := strings.Cut(commit, "-")
	if !ok {
		return "", ""
	}
	width := map[string]int{"sha1": 40, "sha256": 64}[name]
	if width == 0 || len(value) != width {
		return "", ""
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdefABCDEF", r)
	}) >= 0 {
		return "", ""
	}
	return name, strings.ToLower(value)
}
