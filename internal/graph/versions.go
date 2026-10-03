package graph

import (
	"cmp"
	"maps"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Version is one reference in use, what it runs, and who runs it.
type Version struct {
	Reference string `json:"reference"`
	// Commit is what the reference resolves to now, empty where nothing resolved it.
	Commit string `json:"commit"`
	// Queue is where the reference stands in the crawl, empty where it was never queued.
	Queue string `json:"queue"`
	// LockedTo are the commits locks select behind this reference instead, and for how many.
	LockedTo []LockedCommit `json:"lockedTo"`
	// Direct name it themselves and Transitive reach it only through something they name. Never
	// added together, and a repository doing both is direct.
	Direct     int `json:"direct"`
	Transitive int `json:"transitive"`
	// Lines are the `uses:` lines naming it here, among the repositories counted.
	Lines int `json:"lines"`
	// Findings were reported in the dependency's own code at Commit. Nil where no findings run
	// reached that commit, which is not analyzed rather than clean.
	Findings *store.VersionFindings `json:"findings"`
}

// LockedCommit is one commit locks select behind a reference, and how many repositories' locks.
type LockedCommit struct {
	Commit       string `json:"commit"`
	Repositories int    `json:"repositories"`
}

// VersionsPage lists a node's versions with the advisories naming it, verbatim and unjudged.
type VersionsPage struct {
	Key   string    `json:"key"`
	Items []Version `json:"items"`
	// Total is how many references the repositories counted use, before the reference terms
	// narrowed the list. Unlisted are those only repositories left out of the count use.
	Total      int                   `json:"total"`
	Unlisted   int                   `json:"unlisted"`
	Advisories []store.AdvisoryReach `json:"advisories"`
}

// VersionsOf lists each reference a node is used at, counted from the walk the uses table reads
// so the two cannot disagree. The reference terms choose which rows are listed; every other term
// narrows who is counted in them.
func (b *Builder) VersionsOf(key string, q UsesQuery) (VersionsPage, error) {
	page := VersionsPage{Key: key, Items: []Version{}, Advisories: []store.AdvisoryReach{}}
	m, all, err := b.usesRows(key)
	if err != nil || m == nil {
		return page, err
	}
	refs, byRef, unlisted := tallyVersions(all, q)
	page.Total, page.Unlisted = len(refs), unlisted
	if err := b.fillVersions(&page, m, key, refs, byRef, q); err != nil {
		return page, err
	}
	slices.SortStableFunc(page.Items, func(a, b Version) int {
		return cmp.Or(cmp.Compare(b.Direct+b.Transitive, a.Direct+a.Transitive),
			cmp.Compare(b.Lines, a.Lines), cmp.Compare(a.Reference, b.Reference))
	})
	return page, nil
}

// tallyVersions counts each reference's rows under every term but the reference terms, which
// choose which versions are listed rather than who is counted in them. A reference only
// repositories left out of the count use is not listed; how many there were is returned beside.
func tallyVersions(all []Use, q UsesQuery) ([]string, map[string]*versionTally, int) {
	counted := q
	counted.At, counted.RefStyle = nil, ""
	byRef := map[string]*versionTally{}
	refs, used := []string{}, map[string]bool{}
	for _, u := range all {
		if !inUse(u.Hops, u.Reference) {
			continue
		}
		used[u.Reference] = true
		if !counted.keeps(u) {
			continue
		}
		t, ok := byRef[u.Reference]
		if !ok {
			t = newVersionTally()
			byRef[u.Reference] = t
			refs = append(refs, u.Reference)
		}
		t.count(u)
	}
	return refs, byRef, len(used) - len(refs)
}

// inUse reports whether a use names a version. Holding a node is not using it, and a local path
// (`uses: ./`) writes no reference: it runs whatever commit its caller is at.
func inUse(hops int, reference string) bool { return hops > 0 && reference != "" }

func (b *Builder) fillVersions(
	page *VersionsPage, m *model, key string, refs []string, byRef map[string]*versionTally, q UsesQuery,
) error {
	queue, err := b.Store.QueueStatesOf(key)
	if err != nil {
		return err
	}
	commits := make([]string, 0, len(refs))
	for _, ref := range refs {
		commits = append(commits, m.resolved[key+"\x00"+ref])
	}
	findings, err := b.Store.FindingsAtCommits(key, commits)
	if err != nil {
		return err
	}
	if page.Advisories, err = b.Store.AdvisoriesNaming(key); err != nil {
		return err
	}
	listed := q
	listed.Reached, listed.Origins, listed.Repositories = nil, nil, nil
	for _, ref := range refs {
		if !listed.keeps(Use{Reference: ref, Origin: store.OriginSeed}) {
			continue
		}
		v := byRef[ref].version(ref)
		v.Commit, v.Queue = m.resolved[key+"\x00"+ref], queue[ref]
		if f, ok := findings[v.Commit]; ok {
			v.Findings = &f
		}
		page.Items = append(page.Items, v)
	}
	return nil
}

// versionTally gathers one reference's rows before they become a Version.
type versionTally struct {
	direct, transitive map[string]bool
	locked             map[string]map[string]bool
	lines              int
}

func newVersionTally() *versionTally {
	return &versionTally{direct: map[string]bool{}, transitive: map[string]bool{},
		locked: map[string]map[string]bool{}}
}

func (t *versionTally) count(u Use) {
	if u.Hops == 1 {
		t.direct[u.Repository] = true
	} else {
		t.transitive[u.Repository] = true
	}
	t.lines += u.Lines
	if u.Locked {
		if t.locked[u.Commit] == nil {
			t.locked[u.Commit] = map[string]bool{}
		}
		t.locked[u.Commit][u.Repository] = true
	}
}

func (t *versionTally) version(ref string) Version {
	v := Version{Reference: ref, Direct: len(t.direct), Lines: t.lines,
		LockedTo: []LockedCommit{}}
	for repo := range t.transitive {
		if !t.direct[repo] {
			v.Transitive++
		}
	}
	for _, commit := range slices.Sorted(maps.Keys(t.locked)) {
		v.LockedTo = append(v.LockedTo, LockedCommit{Commit: commit, Repositories: len(t.locked[commit])})
	}
	return v
}
