package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// ErrNoMark refuses incremental derivation: stored reach does not say what it was derived from.
var ErrNoMark = errors.New("stored reach carries no mark of what it was derived from")

// Derivation is what one derivation of reach did, in the units its cost scales with.
type Derivation struct {
	Incremental bool
	// Changed counts nodes where what a walk reads differs from what the stored reach read.
	Changed int
	// Walked counts walks: one per node whose reach was derived and one per node whose exposure was.
	Walked int
	// Rows counts rows deleted, inserted or updated in reach, exposure and edge facts.
	Rows int
}

func (d Derivation) plus(o Derivation) Derivation {
	d.Changed, d.Walked, d.Rows = d.Changed+o.Changed, d.Walked+o.Walked, d.Rows+o.Rows
	return d
}

// ReachMarkVersion names the walk a mark was written by. Bump it with any change to the walk, its
// inputs or its writes.
const ReachMarkVersion = "reach-inputs/3"

// buildRevision makes a new build rebuild once rather than trust a mark that other code wrote.
var buildRevision = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if modified == "true" {
		revision += "+modified"
	}
	return revision
})

func (b *Builder) markVersion() string {
	return fmt.Sprintf("%s depth=%d build=%s", ReachMarkVersion, b.MaxDepth, buildRevision())
}

// Rebuild is the oracle: every aggregate wholesale, then the mark the next Derive starts from.
func (b *Builder) Rebuild() (Derivation, error) {
	var d Derivation
	// Classifying first: it only ever fills edges that have none, so it costs nothing once the
	// graph and the classification agree, and the aggregates below can read it.
	if _, err := b.Store.ClassifyEdgeBoundaries(); err != nil {
		return d, err
	}
	// Same shape: the queue already resolved these tags, and recording them is what lets a
	// commit-pinned reference be named as the version GitHub can match an advisory against.
	if _, err := b.Store.BackfillRefResolutions(); err != nil {
		return d, err
	}
	// From nodes alone, so it owes nothing to the walks below and they read none of it.
	if _, err := b.Store.DeriveContainment(); err != nil {
		return d, err
	}
	// One graph for both walks and the mark, so what is recorded as read is what was walked.
	m, err := b.load()
	if err != nil {
		return d, err
	}
	targets, err := b.affectedNodes()
	if err != nil {
		return d, err
	}
	reach, err := b.writeBlastRadius(m)
	if err != nil {
		return d, err
	}
	exposure, err := b.writeExposure(m, targets)
	if err != nil {
		return d, err
	}
	d = reach.plus(exposure)
	// Last, because the advisory fact reads the routes exposure just stored. Deriving it before
	// would leave every edge naming the previous cycle's exposure.
	facts, err := b.Store.DeriveEdgeFacts()
	if err != nil {
		return d, err
	}
	d.Rows += facts

	// Only now: a failure above leaves no mark, and the next derivation rebuilds.
	tx, err := b.Store.BeginDerivation()
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.MarkReach(b.markVersion(), m.reachInputs(targets), true); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// Derive rewrites reach, exposure and the advisory fact only where what the walk reads changed.
// Diffing the walk's inputs rather than listing their writers leaves no writer to forget.
func (b *Builder) Derive() (Derivation, error) {
	d := Derivation{Incremental: true}
	mark, ok, err := b.Store.ReadReachMark()
	if err != nil {
		return d, err
	}
	if !ok || mark.Version != b.markVersion() {
		return d, ErrNoMark
	}
	m, err := b.walkable()
	if err != nil {
		return d, err
	}
	targets, err := b.affectedNodes()
	if err != nil {
		return d, err
	}
	c := change{was: mark.Inputs, now: m.reachInputs(targets), advisories: advisoriesByNode(targets)}
	c.changed = changedInputs(c.was, c.now)
	d.Changed = len(c.changed)
	var moved []int64
	for node := range c.changed {
		if c.was[node].Walk != c.now[node].Walk {
			moved = append(moved, node)
		}
	}
	c.through = m.walksThrough(moved)

	tx, err := b.Store.BeginDerivation()
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()

	reach, err := b.deriveReach(tx, m, c)
	if err != nil {
		return d, err
	}
	exposure, err := b.deriveExposure(tx, m, c)
	if err != nil {
		return d, err
	}
	d = d.plus(reach).plus(exposure)
	// Every edge: one ingested at a commit nothing walks still gets the fact the rebuild gives it.
	facts, err := tx.DeriveAdvisoryRoutes()
	if err != nil {
		return d, err
	}
	d.Rows += facts
	if len(c.changed) > 0 {
		if err := tx.MarkReach(b.markVersion(), c.changed, false); err != nil {
			return d, err
		}
	}
	return d, tx.Commit()
}

// change is what Derive found: inputs then and now, those that differ, and whose walks they touch.
type change struct {
	was, now, changed map[int64]store.ReachInput
	through           map[int64]bool
	advisories        map[int64][]string
}

// deriveReach rewrites reach whose walk may stand at a change, retracting what nothing references.
func (b *Builder) deriveReach(tx store.DerivationWriter, m *model, c change) (Derivation, error) {
	var d Derivation
	if err := tx.PrepareReach(); err != nil {
		return d, err
	}
	for _, node := range slices.Sorted(maps.Keys(c.through)) {
		if c.was[node].Walk == "" && c.now[node].Walk == "" {
			continue
		}
		n, err := tx.DeleteReach(node)
		if err != nil {
			return d, err
		}
		d.Rows += n
		if c.now[node].Walk == "" {
			continue
		}
		n, err = tx.WriteReach(node, storedReach(m.reachOf(node, b.MaxDepth)))
		if err != nil {
			return d, err
		}
		d.Walked, d.Rows = d.Walked+1, d.Rows+n
	}
	return d, nil
}

// deriveExposure rewrites exposure whose walk may stand at a change or whose advisories changed.
func (b *Builder) deriveExposure(tx store.DerivationWriter, m *model, c change) (Derivation, error) {
	var d Derivation
	exposed := map[int64]bool{}
	for node := range c.through {
		if c.was[node].Advisories != "" || c.now[node].Advisories != "" {
			exposed[node] = true
		}
	}
	for node := range c.changed {
		if c.was[node].Advisories != c.now[node].Advisories {
			exposed[node] = true
		}
	}
	if err := tx.PrepareExposure(); err != nil {
		return d, err
	}
	for _, node := range slices.Sorted(maps.Keys(exposed)) {
		n, err := tx.DeleteExposure(node)
		if err != nil {
			return d, err
		}
		d.Rows += n
		// Nothing references a node with no walk input, so its walk would find nobody.
		if c.now[node].Walk == "" || len(c.advisories[node]) == 0 {
			continue
		}
		routes := m.exposureOf(node, b.MaxDepth)
		d.Walked++
		for _, ghsa := range c.advisories[node] {
			n, err := tx.WriteExposure(node, ghsa, storedExposure(routes))
			if err != nil {
				return d, err
			}
			d.Rows += n
		}
	}
	return d, nil
}

// reachInputs is what a derivation reads at each node: its walk digest and covering advisories.
func (m *model) reachInputs(targets []affected) map[int64]store.ReachInput {
	out := make(map[int64]store.ReachInput, len(m.referenceable))
	for _, node := range m.referenceable {
		out[node] = store.ReachInput{Walk: m.walkDigest(node)}
	}
	for node, ghsas := range advisoriesByNode(targets) {
		in := out[node]
		in.Advisories = strings.Join(ghsas, ",")
		out[node] = in
	}
	return out
}

// walkDigest is everything a walk or its visitors read standing at node; a new read belongs here.
func (m *model) walkDigest(node int64) string {
	h := sha256.New()
	key := m.keyOf[node]
	fmt.Fprintf(h, "%q %t %d\n", key, m.isExternal[node], len(m.reverse[node]))
	for _, in := range m.reverse[node] {
		fmt.Fprintf(h, "e %d %d %q %q %q\n", in.row, in.src, in.ref, in.commits,
			m.resolved[key+"\x00"+in.ref])
		for _, l := range m.locks[in.row] {
			fmt.Fprintf(h, "l %q %d %t\n", l.commit, l.consumer, l.own)
		}
		repo, hasRepo := m.repoOf[in.src]
		fmt.Fprintf(h, "s %q %q %t %d %t %t\n", m.kindOf[in.src], m.jobOf[in.src], hasRepo, repo,
			m.seedRepo[repo], m.startsAt(repo, in.commits))
		if c, ok := m.container[in.src]; ok {
			fmt.Fprintf(h, "c %d %q %q %t\n", c, m.keyOf[c], m.kindOf[c], m.isExternal[c])
		} else {
			fmt.Fprintln(h, "c -")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// walksThrough lists every node whose walk may stand at one of moved: each, and what it depends on.
// The graph as it is now suffices: a route only the old graph had ends at a node that moved itself.
func (m *model) walksThrough(moved []int64) map[int64]bool {
	dependsOn := map[int64][]int64{}
	for target, ins := range m.reverse {
		for _, in := range ins {
			if c, ok := m.container[in.src]; ok {
				dependsOn[c] = append(dependsOn[c], target)
			}
		}
	}
	out := map[int64]bool{}
	queue := slices.Clone(moved)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if out[node] {
			continue
		}
		out[node] = true
		queue = append(queue, dependsOn[node]...)
	}
	return out
}

// changedInputs lists nodes whose inputs differ, with the new input; zero means it has none now.
func changedInputs(was, now map[int64]store.ReachInput) map[int64]store.ReachInput {
	out := map[int64]store.ReachInput{}
	for node, in := range now {
		if was[node] != in {
			out[node] = in
		}
	}
	for node := range was {
		if _, ok := now[node]; !ok {
			out[node] = store.ReachInput{}
		}
	}
	return out
}

// advisoriesByNode lists, sorted, the live advisories covering each node.
func advisoriesByNode(targets []affected) map[int64][]string {
	out := map[int64][]string{}
	for _, t := range targets {
		if !slices.Contains(out[t.nodeID], t.ghsaID) {
			out[t.nodeID] = append(out[t.nodeID], t.ghsaID)
		}
	}
	for node := range out {
		slices.Sort(out[node])
	}
	return out
}
