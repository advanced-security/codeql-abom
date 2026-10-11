package graph

import (
	"sync"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Models keeps one loaded graph for the walks that only read it, for as long as the database holds
// what it was loaded from. The zero value is ready to use and safe for concurrent readers.
//
// Keyed by commits rather than by graph generation, because most of what the walk reads changes
// without one: the queue writes resolved commits in place mid-cycle, a declaration promotes a
// repository, a lock is read during analysis, and ingestion lands edges before the cycle records a
// generation. The backend's state token observes writes without relying on their announcements.
type Models struct {
	// mu is held across a load, so readers arriving together share one rather than each paying.
	mu    sync.Mutex
	state store.StateKey
	model *model
	loads int
}

// Loads reports how many times the graph has been read from the database.
func (c *Models) Loads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads
}

func (c *Models) get(b *Builder) (*model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Read before loading: a commit landing during the load leaves the key behind the graph, which
	// costs the next walk a reload and never serves anything older than the key.
	state, err := b.Store.DatabaseState()
	if err != nil {
		return nil, err
	}
	if c.model != nil && c.state == state {
		return c.model, nil
	}
	c.model = nil
	m, err := b.load()
	if err != nil {
		return nil, err
	}
	c.model, c.state = m, state
	c.loads++
	return m, nil
}

// walkable is the graph for a walk that only reads it, shared where the builder has Models.
func (b *Builder) walkable() (*model, error) {
	if b.Models == nil {
		return b.load()
	}
	return b.Models.get(b)
}
