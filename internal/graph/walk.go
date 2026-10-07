package graph

import (
	"slices"
	"strconv"
	"strings"
)

// reachedNode carries the commit and consumer bindings that make a route possible, and the version
// of the walk's target the route runs and the reference that reached it. excludes is sorted and
// comma-joined so reachedNode stays usable as a map key.
type reachedNode struct {
	node     int64
	commit   string
	requires int64
	excludes string
	version  string
	entry    string
}

// step is one reference a walk admitted: what made it, at what depth, the state it was expanded
// from, the bindings it was admitted under, and where the walk continues from — one state per
// commit that declared it, where that is somebody else's dependency.
type step struct {
	depth int
	in    inEdge
	from  reachedNode
	at    reachedNode
	via   reachedNode
	vias  []reachedNode
}

// walk visits, breadth-first from target, every reference by which a dependent reaches it, and
// returns how many levels it walked. Reach, exposure and the node page all stand on it, so they
// cannot disagree about who runs what.
//
// Routes stay inside the dependency commit a consumer runs; exposureagreement_test.go and
// versionreach_test.go hold this to the submission closure, which answers the same question in SQL.
func (m *model) walk(target int64, maxDepth int, visit func(step)) int {
	start := reachedNode{node: target}
	visited := map[reachedNode]bool{start: true}
	frontier := []reachedNode{start}
	levels := 0

	for depth := 1; len(frontier) > 0; depth++ {
		if maxDepth > 0 && depth > maxDepth {
			break
		}
		levels = depth
		var next []reachedNode
		for _, cur := range frontier {
			for _, in := range m.reverse[cur.node] {
				for _, at := range m.admitted(in, cur, depth == 1) {
					vias := m.onward(in, at)
					visit(step{depth: depth, in: in, from: cur, at: at, via: vias[0], vias: vias})
					for _, via := range vias {
						if via.node != in.src && !visited[via] {
							visited[via] = true
							next = append(next, via)
						}
					}
				}
			}
		}
		frontier = next
	}
	return levels
}

// admitted lists the bindings under which a reference reaches cur. Into the target, that is one
// per version of it the reference runs; further out, leaving a dependency requires a consumer that
// can run the commit the route stands at.
func (m *model) admitted(in inEdge, cur reachedNode, intoTarget bool) []reachedNode {
	if intoTarget {
		return m.versionsRun(in, cur.node)
	}
	requires, excludes, ok := m.admits(in, cur)
	if !ok {
		return nil
	}
	cur.requires, cur.excludes = requires, excludes
	return []reachedNode{cur}
}

// versionsRun lists the commits of target a reference runs, each bound as a route into that commit
// is: those a lock selects and the one its ref resolves to. A reference resolving to nothing
// recorded runs a version ABOM has no reading of, which constrains nothing and names no commit. A
// target that is not somebody else's dependency has no version of its own.
func (m *model) versionsRun(in inEdge, target int64) []reachedNode {
	if !m.isExternal[target] {
		return []reachedNode{{node: target, entry: in.ref}}
	}
	resolved := m.resolved[m.keyOf[target]+"\x00"+in.ref]
	candidates := []string{resolved}
	ownLock := false
	for _, l := range m.locks[in.row] {
		candidates = append(candidates, l.commit)
		ownLock = ownLock || l.own
	}
	var out []reachedNode
	for _, commit := range candidates {
		if commit == "" || slices.ContainsFunc(out, func(r reachedNode) bool { return r.commit == commit }) {
			continue
		}
		at := reachedNode{node: target, commit: commit, version: commit, entry: in.ref}
		if requires, excludes, ok := m.admits(in, at); ok {
			at.requires, at.excludes = requires, excludes
			out = append(out, at)
		}
	}
	if resolved == "" && !ownLock {
		unknown := reachedNode{node: target, entry: in.ref}
		for _, l := range m.locks[in.row] {
			unknown.excludes = withListed(unknown.excludes, l.consumer)
		}
		out = append(out, unknown)
	}
	return out
}

// onward lists where a walk continues from a reference: the workflow or composite action holding
// it, which is what a caller references, bound to each commit that declared it where that container
// is somebody else's dependency. A reference with no container continues nowhere.
func (m *model) onward(in inEdge, at reachedNode) []reachedNode {
	via := reachedNode{node: in.src, requires: at.requires, excludes: at.excludes,
		version: at.version, entry: at.entry}
	c, ok := m.container[in.src]
	if !ok {
		return []reachedNode{via}
	}
	via.node = c
	if !m.isExternal[c] {
		return []reachedNode{via}
	}
	vias := make([]reachedNode, 0, len(in.commits))
	for _, commit := range in.commits {
		via.commit = commit
		vias = append(vias, via)
	}
	return vias
}

// countedFor names the repository under review a step counts toward: the one making the reference,
// where the route's lock bindings admit it.
func (m *model) countedFor(s step) (int64, bool) {
	repo, ok := m.boundFor(s)
	return repo, ok && m.seedRepo[repo]
}

// boundFor names the repository making a step's reference, of any origin, where the route's lock
// bindings admit it and, for a repository under review, where it declares the reference at a commit
// it stands at as a starting point.
func (m *model) boundFor(s step) (int64, bool) {
	repo, ok := m.repoOf[s.in.src]
	if !ok || listed(s.at.excludes, repo) || (s.at.requires != 0 && s.at.requires != repo) {
		return 0, false
	}
	if m.seedRepo[repo] && !m.startsAt(repo, s.in.commits) {
		return 0, false
	}
	return repo, true
}

// startsAt reports whether a repository's declaration, made at commits, is its own as a starting
// point. Where no reference resolves the repository nothing says which commit it runs, so any is.
func (m *model) startsAt(repo int64, commits []string) bool {
	starts, ok := m.starts[repo]
	if !ok {
		return true
	}
	return slices.ContainsFunc(commits, func(c string) bool { return starts[c] })
}

// admits reports whether a reference can reach cur.commit and how that binds later routes.
func (m *model) admits(in inEdge, cur reachedNode) (int64, string, bool) {
	if cur.commit == "" {
		return cur.requires, cur.excludes, true
	}
	locks := m.locks[in.row]
	own := false
	for _, l := range locks {
		if !l.own {
			continue
		}
		own = true
		if l.commit == cur.commit {
			return cur.requires, cur.excludes, true
		}
	}
	if own {
		return 0, "", false
	}
	if m.resolved[m.keyOf[cur.node]+"\x00"+in.ref] == cur.commit {
		excludes := cur.excludes
		blocked := false
		for _, l := range locks {
			if l.commit == cur.commit {
				continue
			}
			if cur.requires == l.consumer {
				blocked = true
			}
			excludes = withListed(excludes, l.consumer)
		}
		if !blocked {
			return cur.requires, excludes, true
		}
	}
	for _, l := range locks {
		if l.commit == cur.commit && (cur.requires == 0 || cur.requires == l.consumer) &&
			!listed(cur.excludes, l.consumer) {
			return l.consumer, cur.excludes, true
		}
	}
	return 0, "", false
}

// listed reports whether a comma-joined set of repository ids holds one.
func listed(set string, id int64) bool {
	if set == "" {
		return false
	}
	return slices.Contains(strings.Split(set, ","), strconv.FormatInt(id, 10))
}

// withListed adds a repository id to a comma-joined set, kept sorted so equal sets compare equal.
func withListed(set string, id int64) string {
	if listed(set, id) {
		return set
	}
	ids := []string{strconv.FormatInt(id, 10)}
	if set != "" {
		ids = append(ids, strings.Split(set, ",")...)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}
