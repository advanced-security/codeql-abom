package graph

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// StepCondition is an `if:` on the route, and the container holding the step it guards.
//
// Reported verbatim and never evaluated. Whether `github.ref == 'refs/heads/main'` holds on a
// given run is not something static analysis settles; printing the expression lets a reader
// judge, while deciding it would be this tool inventing a verdict.
type StepCondition struct {
	In        string `json:"in"`
	Condition string `json:"condition"`
}

// Use is one repository running a node at one version, by one reference: a row of the details
// page's "what uses: this" table.
type Use struct {
	Repository string `json:"repository"`
	// Origin is how the repository entered the record, `seed` or `upstream`; never ownership.
	Origin string `json:"origin"`
	// Reference is what the hop into the node wrote, verbatim: the repository's own for a direct
	// use, an intermediary's for a transitive one.
	Reference string `json:"reference"`
	// Commit is the commit that reference runs — what a lock selects, or what it resolves to. Empty
	// where nothing recorded one, which is a version ABOM has no reading of.
	Commit string `json:"commit"`
	// Locked says the repository's lock, or a caller's lock it runs under, selects Commit.
	Locked bool `json:"locked"`
	// Hops is 0 where the node lives in the repository, 1 where it names it, more through others.
	Hops int `json:"hops"`
	// Through is what the repository itself names that brings the node in, empty when direct.
	Through    string          `json:"through"`
	Path       []string        `json:"path"`
	Conditions []StepCondition `json:"conditions"`
	// Workflows are the repository's own workflows and composite actions the route starts from.
	Workflows []string `json:"workflows"`
	// Lines counts the repository's own `uses:` lines naming the node at this reference: the
	// places to edit. Zero for a transitive use, which has none.
	Lines int `json:"lines"`
}

// UsesQuery narrows and orders the table. Every narrowing applies before counting.
type UsesQuery struct {
	At       []string
	RefStyle string
	Reached  []string
	// Origins defaults to `seed`: an upstream repository carries routes but is nobody's to change
	// here, so it is hidden and counted unless asked for.
	Origins []string
	// Repositories bounds the rows to these lowercased full names; nil asks nothing.
	Repositories map[string]bool
	Sort         string
	Desc         bool
	Offset       int
	Limit        int
}

// UsesPage is one page of the table, the size of the match it was cut from, and what it left out.
type UsesPage struct {
	Key   string `json:"key"`
	Items []Use  `json:"items"`
	Total int    `json:"total"`
	// HiddenUpstream counts the upstream repositories the default origin left out.
	HiddenUpstream int `json:"hiddenUpstream"`
	// Unanalyzed counts queued dependencies never analyzed: a route only through one cannot appear.
	Unanalyzed int `json:"unanalyzed"`
	// Change answers what moving the one reference asked for would do. Nil unless exactly one is.
	Change *TagChange `json:"change,omitempty"`
}

// TagChange is who would run a different commit if one reference moved, by repository.
type TagChange struct {
	Reference string `json:"reference"`
	// Fixed means the reference is a commit, which does not move; nothing else is said.
	Fixed bool `json:"fixed"`
	// NamesNow is the commit the reference resolves to, empty where nothing resolved it.
	NamesNow string `json:"namesNow"`
	// Direct name it themselves and Transitive reach it through something else; never added up.
	Direct     []string `json:"direct"`
	Transitive []string `json:"transitive"`
	// Kept are the repositories whose every use of it a lock pins, so moving it moves nothing there.
	Kept []string `json:"kept"`
}

var commitID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// usesSorts are the orders the table offers. A sort nobody declared is refused, not ignored.
var usesSorts = map[string]func(a, b Use) int{
	"repository": func(a, b Use) int { return cmp.Compare(a.Repository, b.Repository) },
	"reference":  func(a, b Use) int { return cmp.Compare(a.Reference, b.Reference) },
	"commit":     func(a, b Use) int { return cmp.Compare(a.Commit, b.Commit) },
	"locked":     func(a, b Use) int { return cmpBool(a.Locked, b.Locked) },
	"relationship": func(a, b Use) int {
		return cmp.Compare(a.Hops, b.Hops)
	},
	"through":    func(a, b Use) int { return cmp.Compare(a.Through, b.Through) },
	"workflows":  func(a, b Use) int { return cmp.Compare(len(a.Workflows), len(b.Workflows)) },
	"lines":      func(a, b Use) int { return cmp.Compare(a.Lines, b.Lines) },
	"conditions": func(a, b Use) int { return cmp.Compare(len(a.Conditions), len(b.Conditions)) },
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// UsesSortNames lists what the table may be sorted on, so the interface offers exactly that.
func UsesSortNames() []string { return slices.Sorted(maps.Keys(usesSorts)) }

// UsesOf answers who uses one node, at which reference and commit, from the walk reach and
// exposure take, so the table cannot disagree with either.
func (b *Builder) UsesOf(key string, q UsesQuery) (UsesPage, error) {
	page := UsesPage{Key: key, Items: []Use{}}
	order, ok := usesSorts[q.Sort]
	if q.Sort != "" && !ok {
		return page, fmt.Errorf("cannot sort uses by %q; it sorts by one of %v", q.Sort, UsesSortNames())
	}
	var err error
	if page.Unanalyzed, err = b.Store.UnanalyzedDependencies(); err != nil {
		return page, err
	}
	m, all, err := b.usesRows(key)
	if err != nil || m == nil {
		return page, err
	}
	matched, hidden := q.narrow(all)
	page.HiddenUpstream = hidden
	page.Change = m.tagChange(key, q.At, matched)
	sortUses(matched, order, q.Desc)
	page.Total = len(matched)
	page.Items = q.cut(matched)
	return page, nil
}

// usesRows walks one node and returns every row, of every origin, before any narrowing. A nil
// model means the graph holds no such node, which is a fair question with an empty answer.
func (b *Builder) usesRows(key string) (*model, []Use, error) {
	m, err := b.load()
	if err != nil {
		return nil, nil, err
	}
	target, found := m.nodeNamed(key)
	if !found {
		return nil, nil, nil
	}
	names, err := repositoryNames(b.Store)
	if err != nil {
		return nil, nil, err
	}
	return m, m.usesOf(target, b.MaxDepth, names), nil
}

// narrow keeps the rows the query asks for, and counts the upstream repositories the default
// origin left out rather than dropping them in silence.
func (q UsesQuery) narrow(all []Use) ([]Use, int) {
	upstream := q
	upstream.Origins = []string{store.OriginUpstream}
	matched, hidden := []Use{}, map[string]bool{}
	for _, u := range all {
		switch {
		case q.keeps(u):
			matched = append(matched, u)
		case len(q.Origins) == 0 && upstream.keeps(u):
			hidden[u.Repository] = true
		}
	}
	return matched, len(hidden)
}

// sortUses orders the whole match, never a page of it: the default first, then the asked column.
func sortUses(rows []Use, order func(a, b Use) int, desc bool) {
	slices.SortStableFunc(rows, defaultUseOrder)
	if order == nil {
		return
	}
	slices.SortStableFunc(rows, func(a, b Use) int {
		if desc {
			return order(b, a)
		}
		return order(a, b)
	})
}

// cut takes one page out of the match.
func (q UsesQuery) cut(rows []Use) []Use {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	start := min(max(q.Offset, 0), len(rows))
	return rows[start:min(start+limit, len(rows))]
}

func defaultUseOrder(a, b Use) int {
	return cmp.Or(cmp.Compare(a.Hops, b.Hops), cmp.Compare(a.Repository, b.Repository),
		cmp.Compare(a.Reference, b.Reference), cmp.Compare(a.Commit, b.Commit))
}

// keeps applies every narrowing a row is subject to. With no origin asked it keeps starting points.
func (q UsesQuery) keeps(u Use) bool {
	origins := q.Origins
	if len(origins) == 0 {
		origins = []string{store.OriginSeed}
	}
	return slices.Contains(origins, u.Origin) &&
		(q.Repositories == nil || q.Repositories[strings.ToLower(u.Repository)]) &&
		(len(q.At) == 0 || slices.Contains(q.At, u.Reference)) &&
		styleKeeps(q.RefStyle, u.Reference) && reachedKeeps(q.Reached, u.Hops)
}

// styleKeeps reads a reference's shape lexically, which is all the style term ever asks.
func styleKeeps(style, reference string) bool {
	switch style {
	case "sha":
		return commitID.MatchString(reference)
	case "tag":
		return reference != "" && !commitID.MatchString(reference)
	}
	return true
}

// reachedKeeps answers the reached term. A node living in the repository is neither.
func reachedKeeps(reached []string, hops int) bool {
	if len(reached) == 0 {
		return true
	}
	return hops == 1 && slices.Contains(reached, "direct") ||
		hops > 1 && slices.Contains(reached, "transitive")
}

// tagChange says what moving the one reference asked for would do to the matched rows.
func (m *model) tagChange(key string, at []string, rows []Use) *TagChange {
	if len(at) != 1 {
		return nil
	}
	c := &TagChange{Reference: at[0], Direct: []string{}, Transitive: []string{}, Kept: []string{}}
	if commitID.MatchString(at[0]) {
		c.Fixed = true
		return c
	}
	c.NamesNow = m.resolved[key+"\x00"+at[0]]
	moves, direct, kept := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range rows {
		if u.Locked {
			kept[u.Repository] = true
			continue
		}
		moves[u.Repository] = true
		if u.Hops == 1 {
			direct[u.Repository] = true
		}
	}
	for repo := range moves {
		if direct[repo] {
			c.Direct = append(c.Direct, repo)
		} else {
			c.Transitive = append(c.Transitive, repo)
		}
		delete(kept, repo)
	}
	for repo := range kept {
		c.Kept = append(c.Kept, repo)
	}
	slices.Sort(c.Direct)
	slices.Sort(c.Transitive)
	slices.Sort(c.Kept)
	return c
}

// repository is a repository's name and how it entered the record.
type repository struct{ name, origin string }

func repositoryNames(s *store.Store) (map[int64]repository, error) {
	rows, err := s.DB().Query(`SELECT row_id, repository_full_name, COALESCE(origin, '') FROM repository`)
	if err != nil {
		return nil, fmt.Errorf("naming repositories: %w", err)
	}
	defer rows.Close()
	out := map[int64]repository{}
	for rows.Next() {
		var id int64
		var r repository
		if err := rows.Scan(&id, &r.name, &r.origin); err != nil {
			return nil, fmt.Errorf("scanning a repository: %w", err)
		}
		out[id] = r
	}
	return out, rows.Err()
}

func (m *model) nodeNamed(key string) (int64, bool) {
	for id, k := range m.keyOf {
		if k == key {
			return id, true
		}
	}
	return 0, false
}

// useKey is a row's identity: one repository, one reference written, one commit run.
type useKey struct {
	repo           int64
	entry, version string
}

// usesOf collects every row the walk reaches, for every repository, before any narrowing.
//
// A step or job is referenced by nothing; it runs because its container was invoked, so the walk
// starts there and the node inherits its callers. The repository holding the node runs it at no
// hops, which is not a reference of its own.
func (m *model) usesOf(node int64, maxDepth int, names map[int64]repository) []Use {
	c := &useCollector{m: m, target: node, names: names, rows: map[useKey]*Use{},
		lines: map[useKey]map[int64]bool{}, from: map[reachedNode]reachedNode{},
		enteredBy: map[reachedNode]int64{}}
	if container, ok := m.container[node]; ok {
		c.target = container
	}
	if repo, ok := m.repoOf[node]; ok {
		c.add(useKey{repo: repo}, &Use{Repository: names[repo].name, Origin: names[repo].origin,
			Path: []string{m.keyOf[node]}, Conditions: []StepCondition{}, Workflows: []string{}})
	}
	m.walk(c.target, maxDepth, c.visit)
	return c.list()
}

// useCollector gathers a walk's rows. Each keeps its fewest hops and the route that gave them, and
// predecessors are kept per walk state rather than per node, so a version's route is the one that
// version was reached by.
type useCollector struct {
	m         *model
	target    int64
	names     map[int64]repository
	rows      map[useKey]*Use
	order     []useKey
	lines     map[useKey]map[int64]bool
	from      map[reachedNode]reachedNode
	enteredBy map[reachedNode]int64
}

func (c *useCollector) add(k useKey, u *Use) {
	c.rows[k] = u
	c.order = append(c.order, k)
}

func (c *useCollector) visit(s step) {
	for _, via := range s.vias {
		if _, seen := c.from[via]; !seen && via.node != c.target {
			c.from[via], c.enteredBy[via] = s.from, s.in.src
		}
	}
	repo, ok := c.m.boundFor(s)
	if !ok {
		return
	}
	k := useKey{repo: repo, entry: s.at.entry, version: s.at.version}
	u, seen := c.rows[k]
	if !seen {
		u = c.first(s, repo)
		c.add(k, u)
	}
	if workflow := c.m.keyOf[s.via.node]; c.m.repoOf[s.via.node] == repo && !slices.Contains(u.Workflows, workflow) {
		u.Workflows = append(u.Workflows, workflow)
	}
	if s.depth == 1 {
		if c.lines[k] == nil {
			c.lines[k] = map[int64]bool{}
		}
		c.lines[k][s.in.row] = true
	}
}

// first is a row as the walk first reached it, which breadth-first is by the fewest hops.
func (c *useCollector) first(s step, repo int64) *Use {
	path, conditions := c.m.routeOf(s.via, c.from, c.enteredBy)
	u := &Use{
		Repository: c.names[repo].name, Origin: c.names[repo].origin,
		Reference: s.at.entry, Commit: s.at.version, Hops: s.depth,
		Locked: c.m.lockedFor(c.target, repo, s.at.entry, s.at.version),
		Path:   path, Conditions: conditions, Workflows: []string{},
	}
	if len(path) > 2 {
		u.Through = path[1]
	}
	return u
}

func (c *useCollector) list() []Use {
	out := make([]Use, 0, len(c.order))
	for _, k := range c.order {
		u := c.rows[k]
		u.Lines = len(c.lines[k])
		slices.Sort(u.Workflows)
		out = append(out, *u)
	}
	return out
}

// lockedFor reports whether a lock that binds this repository selects the commit for a reference
// into the target: its own lock on its own `uses:`, or its lock reaching an intermediary's.
func (m *model) lockedFor(target, repo int64, entry, commit string) bool {
	if commit == "" {
		return false
	}
	for _, in := range m.reverse[target] {
		if in.ref != entry {
			continue
		}
		for _, l := range m.locks[in.row] {
			if l.commit == commit && l.consumer == repo {
				return true
			}
		}
	}
	return false
}

// routeOf walks the recorded predecessor states back to the target, collecting the containers
// passed through and the `if:` guarding each hop, nearest the repository first.
func (m *model) routeOf(
	start reachedNode, from map[reachedNode]reachedNode, enteredBy map[reachedNode]int64,
) ([]string, []StepCondition) {
	path := []string{m.keyOf[start.node]}
	conditions := []StepCondition{}
	seen := map[reachedNode]bool{start: true}
	for state := start; ; {
		if condition := m.conditionOf[enteredBy[state]]; condition != "" {
			conditions = append(conditions, StepCondition{In: m.keyOf[state.node], Condition: condition})
		}
		prev, ok := from[state]
		if !ok || seen[prev] {
			break
		}
		seen[prev] = true
		path = append(path, m.keyOf[prev.node])
		state = prev
	}
	return path, conditions
}
