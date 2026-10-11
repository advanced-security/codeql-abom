package store

import (
	"context"
	"database/sql"
	"fmt"
)

// TraversalStore supplies measured inputs; traversal and classification remain in graph.
type TraversalStore interface {
	DatabaseState() (StateKey, error)
	SeedRepositoryIDs() ([]int64, error)
	TraversalNodes() ([]TraversalNode, error)
	TraversalRepositories(seedOnly bool) ([]NamedRepository, error)
	FrontierResolutions() ([]ResolvedReference, error)
	StepConditions() ([]NodeCondition, error)
	NodeID(key string) (int64, error)
	OrderedRepositories() ([]NamedRepository, error)
	RepositoryMembership() ([]RepositoryMember, error)
	RepositoryDependencies() ([]RepositoryDependency, error)
	DependencyCalls() ([]DependencyCall, error)
	AdvisoryTargets() ([]AdvisoryTarget, error)
	SeedDependencies() ([]Item, error)
	DiscoverySources() ([]DiscoverySource, error)
	DependenciesToQueue(repository string) ([]Item, error)
	AggregateCounts() (map[string]int, error)
	CorpusCounts() ([]LabeledCount, error)
	BeginDerivation() (DerivationWriter, error)
}

// ErrNotFound preserves the existing missing-row answer without requiring consumers to know SQL.
var ErrNotFound = sql.ErrNoRows

// StateKey is comparable and opaque; equality means a cached traversal can still be reused.
type StateKey struct{ token any }

// Both counters restart with a connection; its identity prevents reusing another connection's key.
type sqliteState struct {
	conn                 any
	changes, dataVersion int64
}

func (s *Store) DatabaseState() (StateKey, error) {
	if s.db.postgres {
		var revision int64
		err := s.db.QueryRow(`SELECT revision FROM abom_store_revision WHERE singleton = 1`).Scan(&revision)
		return StateKey{token: sqliteState{conn: s.db, changes: revision}}, err
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return StateKey{}, fmt.Errorf("reserving a connection to read the database state: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var state sqliteState
	if err := conn.Raw(func(driverConn any) error {
		state.conn = driverConn
		return nil
	}); err != nil {
		return StateKey{}, fmt.Errorf("identifying the connection: %w", err)
	}
	if err := conn.QueryRowContext(ctx,
		`SELECT total_changes(), data_version FROM pragma_data_version`).Scan(
		&state.changes, &state.dataVersion); err != nil {
		return StateKey{}, fmt.Errorf("reading the database state: %w", err)
	}
	return StateKey{token: state}, nil
}

type TraversalNode struct {
	ID                        int64
	Key, Kind                 string
	RepositoryID, ContainerID *int64
	External                  bool
	Job                       string
}

type NamedRepository struct {
	ID   int64
	Name string
}

type RepositoryMember struct {
	ID           int64
	Name, Origin string
	Owned        bool
}

type ResolvedReference struct{ Key, Ref, SHA string }
type NodeCondition struct {
	ID        int64
	Condition string
}
type RepositoryDependency struct{ From, To string }
type DependencyCall struct {
	Container, Target int64
	Key, Origin       string
}
type AdvisoryTarget struct {
	GHSAID string
	NodeID int64
}
type DiscoverySource struct {
	Repository string
	Depth      int
}
type LabeledCount struct {
	Label string
	Count int
}

func (s *Store) SeedRepositoryIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT row_id FROM repository WHERE origin = ?`, OriginSeed)
	if err != nil {
		return nil, fmt.Errorf("loading repositories: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (int64, error) {
		var id int64
		if err := r.Scan(&id); err != nil {
			return 0, fmt.Errorf("scanning repository: %w", err)
		}
		return id, nil
	})
}

func (s *Store) TraversalNodes() ([]TraversalNode, error) {
	rows, err := s.db.Query(`SELECT row_id, node_key, node_kind, repository_row_id,
		container_node_row_id, is_external, COALESCE(job_id, '') FROM abom_node_live`)
	if err != nil {
		return nil, fmt.Errorf("loading nodes: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (TraversalNode, error) {
		var n TraversalNode
		if err := r.Scan(&n.ID, &n.Key, &n.Kind, &n.RepositoryID, &n.ContainerID, &n.External, &n.Job); err != nil {
			return n, fmt.Errorf("scanning node: %w", err)
		}
		return n, nil
	})
}

func (s *Store) TraversalRepositories(seedOnly bool) ([]NamedRepository, error) {
	query := `SELECT row_id, LOWER(repository_full_name) FROM repository`
	what, scan := "loading repositories", "scanning a repository"
	var args []any
	if seedOnly {
		query += ` WHERE origin = ?`
		args = append(args, OriginSeed)
		what, scan = "loading repositories under review", "scanning a repository under review"
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return collectRows(rows, func(r *sql.Rows) (NamedRepository, error) {
		var n NamedRepository
		if err := r.Scan(&n.ID, &n.Name); err != nil {
			return n, fmt.Errorf("%s: %w", scan, err)
		}
		return n, nil
	})
}

func (s *Store) FrontierResolutions() ([]ResolvedReference, error) {
	rows, err := s.db.Query(`SELECT node_key, COALESCE(ref, ''), resolved_sha
		FROM abom_frontier WHERE COALESCE(resolved_sha, '') <> ''`)
	if err != nil {
		return nil, fmt.Errorf("loading reference resolutions: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (ResolvedReference, error) {
		var ref ResolvedReference
		if err := r.Scan(&ref.Key, &ref.Ref, &ref.SHA); err != nil {
			return ref, fmt.Errorf("scanning reference resolution: %w", err)
		}
		return ref, nil
	})
}

func (s *Store) StepConditions() ([]NodeCondition, error) {
	rows, err := s.db.Query(ConditionObservationsQuery, SubjectNode, ObservationCondition)
	if err != nil {
		return nil, fmt.Errorf("loading step conditions: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (NodeCondition, error) {
		var c NodeCondition
		if err := r.Scan(&c.ID, &c.Condition); err != nil {
			return c, fmt.Errorf("scanning step condition: %w", err)
		}
		return c, nil
	})
}

func (s *Store) NodeID(key string) (int64, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT row_id FROM abom_node_live WHERE node_key = ?`, key).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving %q: %w", key, err)
	}
	return id, nil
}

func (s *Store) OrderedRepositories() ([]NamedRepository, error) {
	rows, err := s.db.Query(`SELECT row_id, repository_full_name FROM repository ORDER BY repository_full_name`)
	if err != nil {
		return nil, fmt.Errorf("loading repositories: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (NamedRepository, error) {
		var n NamedRepository
		err := r.Scan(&n.ID, &n.Name)
		return n, err
	})
}

func (s *Store) RepositoryMembership() ([]RepositoryMember, error) {
	rows, err := s.db.Query(`SELECT r.row_id, r.repository_full_name, COALESCE(r.origin, ''), ` +
		OwnedRepositoryFilter + ` FROM repository r`)
	if err != nil {
		return nil, fmt.Errorf("naming repositories: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (RepositoryMember, error) {
		var n RepositoryMember
		if err := r.Scan(&n.ID, &n.Name, &n.Origin, &n.Owned); err != nil {
			return n, fmt.Errorf("scanning a repository: %w", err)
		}
		return n, nil
	})
}

func (s *Store) RepositoryDependencies() ([]RepositoryDependency, error) {
	rows, err := s.db.Query(`
		SELECT r.repository_full_name, dst.owner || '/' || dst.repo
		FROM abom_edge_regular e
		JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE ` + DependencyEdgeFilter + `
		  AND dst.owner IS NOT NULL AND dst.repo IS NOT NULL
		  AND dst.owner <> '' AND dst.repo <> ''
		GROUP BY r.repository_full_name, dst.owner || '/' || dst.repo`)
	if err != nil {
		return nil, fmt.Errorf("loading dependency edges: %w", err)
	}
	out, err := collectRows(rows, func(r *sql.Rows) (RepositoryDependency, error) {
		var d RepositoryDependency
		if err := r.Scan(&d.From, &d.To); err != nil {
			return d, fmt.Errorf("scanning a dependency edge: %w", err)
		}
		return d, nil
	})
	return out, err
}

func (s *Store) DependencyCalls() ([]DependencyCall, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(src.container_node_row_id, src.row_id), e.dst_node_row_id,
		       dst.node_key, r.origin
		FROM abom_edge_regular e
		JOIN abom_node_live src ON src.row_id = e.src_node_row_id
		JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE ` + DependencyEdgeFilter)
	if err != nil {
		return nil, fmt.Errorf("loading dependency calls: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (DependencyCall, error) {
		var c DependencyCall
		if err := r.Scan(&c.Container, &c.Target, &c.Key, &c.Origin); err != nil {
			return c, fmt.Errorf("scanning dependency call: %w", err)
		}
		return c, nil
	})
}

func (s *Store) AdvisoryTargets() ([]AdvisoryTarget, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT a.ghsa_id, n.row_id
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		JOIN abom_node_live n
		  ON n.owner IS NOT NULL AND n.repo IS NOT NULL
		 AND n.owner || '/' || n.repo = p.package_name
		WHERE a.withdrawn_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("listing affected nodes: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (AdvisoryTarget, error) {
		var a AdvisoryTarget
		if err := r.Scan(&a.GHSAID, &a.NodeID); err != nil {
			return a, fmt.Errorf("scanning affected node: %w", err)
		}
		return a, nil
	})
}

// dependencyTarget preserves remote owned references alongside external nodes.
const dependencyTarget = `((n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL)
	OR (n.is_external = 0 AND COALESCE(e.ref_value, '') <> '' AND e.ref_raw LIKE '%_/_%@_%'
	    AND e.ref_raw NOT LIKE './%' AND e.ref_raw NOT LIKE '../%' AND e.ref_raw NOT LIKE '$/%'
	    AND e.ref_raw NOT LIKE 'docker://%'))`

func (s *Store) SeedDependencies() ([]Item, error) {
	rows, err := s.db.Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       MIN(r.repository_full_name), COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE ` + OwnedRepositoryFilter + ` AND ` + dependencyTarget + `
		GROUP BY n.node_key, n.owner, n.repo, n.path, COALESCE(e.ref_value, '')
		ORDER BY n.node_key, COALESCE(e.ref_value, '')`)
	if err != nil {
		return nil, fmt.Errorf("reading seed dependencies: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (Item, error) {
		var it Item
		if err := r.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath, &it.DiscoveredFrom, &it.Ref); err != nil {
			return it, fmt.Errorf("scanning seed dependency: %w", err)
		}
		return it, nil
	})
}

func (s *Store) DiscoverySources() ([]DiscoverySource, error) {
	rows, err := s.db.Query(`
		SELECT r.repository_full_name, COALESCE(MIN(f.depth), 0)
		FROM repository r
		LEFT JOIN abom_frontier f ON f.owner || '/' || f.repo = r.repository_full_name
		GROUP BY r.repository_full_name
		ORDER BY r.repository_full_name`)
	if err != nil {
		return nil, fmt.Errorf("listing analyzed repositories: %w", err)
	}
	return collectRows(rows, func(r *sql.Rows) (DiscoverySource, error) {
		var src DiscoverySource
		err := r.Scan(&src.Repository, &src.Depth)
		return src, err
	})
}

func (s *Store) DependenciesToQueue(repository string) ([]Item, error) {
	rows, err := s.db.Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.repository_full_name = ? AND `+dependencyTarget+`
		GROUP BY n.node_key, n.owner, n.repo, n.path, COALESCE(e.ref_value, '')
		ORDER BY n.node_key, COALESCE(e.ref_value, '')`, repository)
	if err != nil {
		return nil, fmt.Errorf("reading discoveries for %s: %w", repository, err)
	}
	return collectRows(rows, func(r *sql.Rows) (Item, error) {
		var it Item
		err := r.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath, &it.Ref)
		return it, err
	})
}

func (s *Store) AggregateCounts() (map[string]int, error) {
	out := make(map[string]int, len(reachTables)+1)
	for _, name := range append(append([]string{}, reachTables...), "abom_exposure") {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s: %w", name, err)
		}
		out[name] = n
	}
	return out, nil
}

func (s *Store) CorpusCounts() ([]LabeledCount, error) {
	out := []LabeledCount{}
	for _, c := range []struct{ label, query string }{
		{"repositories", `SELECT COUNT(*) FROM repository`},
		{"owned", `SELECT COUNT(*) FROM repository WHERE origin = 'seed'`},
		{"nodes", `SELECT COUNT(*) FROM abom_node_live`},
		{"edges", `SELECT COUNT(*) FROM abom_edge_regular`},
		{"findings", `SELECT COUNT(*) FROM abom_finding_regular`},
		{"findings on a node", `SELECT COUNT(DISTINCT fn.finding_row_id) FROM abom_finding_node fn
			JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id`},
		{"advisories", `SELECT COUNT(*) FROM abom_advisory`},
		{"advisories reaching", `SELECT COUNT(DISTINCT a.ghsa_id) FROM abom_advisory a
			JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
			JOIN abom_node_live n ON n.owner || '/' || n.repo = p.package_name`},
		{"job facts", `SELECT COUNT(*) FROM abom_observation`},
		{"privileged jobs", `SELECT COUNT(*) FROM abom_observation WHERE name = 'privileged' AND value = 'true'`},
	} {
		var n int
		if err := s.db.QueryRow(c.query).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s: %w", c.label, err)
		}
		out = append(out, LabeledCount{Label: c.label, Count: n})
	}
	return out, nil
}
