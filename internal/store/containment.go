package store

import (
	"fmt"
	"sort"
)

// What holds what. Neither is declared anywhere: no `uses:` line says a repository holds its
// workflows, and nothing at all says an account holds its repositories.
const (
	EdgeKindRepoContains = "repo-contains"
	EdgeKindOrgContains  = "org-contains"
)

// Containment is one holding, as a pair of keys. It has no identity of its own: nothing declares
// it, so there is no source location to name it by and no second row to tell apart from the first.
type Containment struct {
	OwnerKey string
	HeldKey  string
	Kind     string
}

// DeriveContainment stores what holds what, and reports how many holdings changed.
//
// Derived from the unfiltered projection by the same rule the canvas draws by, so there is one
// answer rather than a table and a drawing that can drift apart. Nodes are read undivided: the
// projection splits a dependency per version, and storing one holding per version would make the
// table depend on which versions a request happened to draw.
//
// Idempotent: only the difference is written, so a second pass reports zero.
func (s *Store) DeriveContainment() (int, error) {
	// Every node rather than the collapsed display: what a repository holds is a fact about the
	// corpus, not about how a reader chose to look at it.
	nodes, _, err := s.graphNodes(identityDisplay, 0, FullGraphOptions{})
	if err != nil {
		return 0, fmt.Errorf("reading nodes to derive containment: %w", err)
	}
	// A repository the operator declared and nothing has analyzed is still held by its account.
	// Leaving it out would make an account's holdings depend on how far the crawl has got.
	nodes, err = s.attachOwnedRepositories(nodes, FullGraphOptions{})
	if err != nil {
		return 0, fmt.Errorf("reading declared repositories to derive containment: %w", err)
	}
	return s.storeContainment(containmentOf(nodes))
}

// containmentOf is the rule, written once and read by both the derivation and the drawing.
func containmentOf(nodes []GraphNode) []Containment {
	owners := map[string]string{}
	represented := map[string]bool{}
	for i := range nodes {
		if nodes[i].Kind == "repo" && nodes[i].Repository != "" {
			owners[nodes[i].Repository] = nodes[i].Key
			represented[nodes[i].Repository] = true
		}
	}

	out := []Containment{}
	for i := range nodes {
		n := &nodes[i]
		// A repository holds what sits directly beneath it. Wiring every step to it as well would
		// add tens of thousands of holdings saying nothing the container tree does not.
		if owner, ok := owners[n.Repository]; ok &&
			n.Kind != "repo" && n.Container == "" && owner != n.Key {
			out = append(out, Containment{
				OwnerKey: owner, HeldKey: n.Key, Kind: EdgeKindRepoContains,
			})
		}
		if owner := ownerOf(n); owner != "" && holdsDirectly(n, represented) {
			out = append(out, Containment{
				OwnerKey: OrgKey(owner), HeldKey: n.Key, Kind: EdgeKindOrgContains,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OwnerKey != out[j].OwnerKey {
			return out[i].OwnerKey < out[j].OwnerKey
		}
		if out[i].HeldKey != out[j].HeldKey {
			return out[i].HeldKey < out[j].HeldKey
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// Containment reads every stored holding.
func (s *Store) Containment() ([]Containment, error) { return s.containmentSince(0) }

// containmentSince reads the holdings recorded after a cursor, which is what a delta carries.
//
// Row ids are the cursor, and only the difference is ever written, so a holding that has not
// changed keeps its row id and is not re-sent.
func (s *Store) containmentSince(since int64) ([]Containment, error) {
	rows, err := s.db.Query(
		`SELECT owner_key, held_key, edge_kind FROM abom_containment
		  WHERE row_id > ? ORDER BY row_id`, since)
	if err != nil {
		return nil, fmt.Errorf("reading containment: %w", err)
	}
	defer rows.Close()

	out := []Containment{}
	for rows.Next() {
		var c Containment
		if err := rows.Scan(&c.OwnerKey, &c.HeldKey, &c.Kind); err != nil {
			return nil, fmt.Errorf("scanning a holding: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// storeContainment writes the difference between what is stored and what the rule now says.
//
// The difference rather than a clear and reload, because a delta cursor is a row id: rewriting
// unchanged rows would re-send the whole containment tree to every open browser every cycle.
func (s *Store) storeContainment(want []Containment) (int, error) {
	have, err := s.Containment()
	if err != nil {
		return 0, err
	}
	stored := map[Containment]bool{}
	for _, c := range have {
		stored[c] = true
	}
	wanted := map[Containment]bool{}
	for _, c := range want {
		wanted[c] = true
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("storing containment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	changed := 0
	for _, c := range want {
		if stored[c] {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO abom_containment (owner_key, held_key, edge_kind)
			 VALUES (?, ?, ?)`, c.OwnerKey, c.HeldKey, c.Kind); err != nil {
			return 0, fmt.Errorf("storing a holding: %w", err)
		}
		changed++
	}
	for _, c := range have {
		if wanted[c] {
			continue
		}
		if _, err := tx.Exec(
			`DELETE FROM abom_containment
			  WHERE owner_key = ? AND held_key = ? AND edge_kind = ?`,
			c.OwnerKey, c.HeldKey, c.Kind); err != nil {
			return 0, fmt.Errorf("removing a holding: %w", err)
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storing containment: %w", err)
	}
	return changed, nil
}

// containmentEdges draws the stored holdings over the nodes a projection actually holds.
//
// Stored keys name undivided nodes, so a dependency drawn once per version takes a holding per
// version here. That expansion is the only thing this decides: what is drawn is always a subset
// of what is stored, never a holding recomputed against whichever nodes survived a filter.
func containmentEdges(nodes []GraphNode, held []Containment) []GraphEdge {
	drawnAs := map[string][]string{}
	for i := range nodes {
		base := refBaseKey(nodes[i].Key, nodes[i].Ref)
		drawnAs[base] = append(drawnAs[base], nodes[i].Key)
	}

	out := []GraphEdge{}
	for _, c := range held {
		for _, target := range drawnAs[c.HeldKey] {
			out = append(out, GraphEdge{
				Source: c.OwnerKey, Target: target, Kind: c.Kind,
				Refs: []string{}, Count: 1,
			})
		}
	}
	return out
}
