package store

import (
	"fmt"
	"strings"
)

// LockSelection is a dependency a lock selects at a commit other than the ref its source
// declares: what the workflow runs, rather than what it says.
type LockSelection struct {
	NodeKey string
	Owner   string
	Repo    string
	Path    string
	// Commit is the lock's digest, and the ref the frontier checks out.
	Commit string
}

// Rules a lock governs an edge by, as LockGoverned.Rule names them.
const (
	// LockRuleWorkflow is a step in one of the repository's own workflows.
	LockRuleWorkflow = "workflow"
	// LockRuleOwnComposite is a step inside the repository's own `./` or `$/` composite.
	LockRuleOwnComposite = "own-composite"
	// LockRuleCaller is a step inside a composite somebody else's lock reaches.
	LockRuleCaller = "caller"
)

// LockGoverned is one edge a current lock governs, and what that lock selects for it.
//
// Three ways a lock governs an edge, and only these three, as the pinned CLI writes them:
//
//   - A step in one of the repository's own workflows is governed by that repository's lock,
//     for that workflow only. The same pin locked for another workflow is that workflow's
//     answer, and borrowing it would describe code this one does not run.
//   - A step inside one of the repository's own composites (`./` or `$/`) is governed by the
//     lock of each workflow that reaches the composite, which lists the composite's pins as its
//     own. A workflow that never calls the composite has no say in it.
//   - A step inside a composite action somebody else calls is governed by the caller's lock.
//     The CLI records a remote composite's nested pins in its caller's DAG and never reads the
//     callee's lock, so the composite's own lock says nothing here. That governs the step only
//     for that caller: another caller, locked or not, runs what its own answer names.
//
// Only locks read at a commit their repository currently resolves to count: a lock the queue
// has moved past describes code nobody runs. A selection the declared ref already names is not
// reported, and only SHA-1 digests are, because nothing here can fetch another kind.
type LockGoverned struct {
	EdgeID  int64
	EdgeKey string
	// Repository is whose edge it is: the repository whose source declares the step.
	Repository  string
	NodeKey     string
	Owner       string
	Repo        string
	Path        string
	DeclaredRef string
	// Commit is what the lock selects, and what that consumer runs.
	Commit string
	// Consumer is whose lock selects it, read at ConsumerCommit. The repository itself unless
	// Rule is LockRuleCaller.
	Consumer       string
	ConsumerCommit string
	// Context is the governing workflow for the repository's own locks, and the caller's pin of
	// the composite for a caller's.
	Context string
	Rule    string
}

// LockSelectedDependencies reports what locks select for the external dependencies one analyzed
// repository names. analyzed is the commit being settled, which the queue has not recorded yet
// and whose lock therefore counts as current.
func (s *Store) LockSelectedDependencies(repository, analyzed string) ([]LockSelection, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok {
		return []LockSelection{}, nil
	}
	// Asked first, by key, because almost nothing is locked: without it every settle would pay
	// for the frontier walk below on behalf of a lock that does not exist.
	var governed int
	if err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM abom_lock_workflow_pin WHERE repository = ?1)
		    OR EXISTS(SELECT 1 FROM abom_lock_dependency WHERE owner = LOWER(?2) AND repo = LOWER(?3))`,
		repository, owner, repo).Scan(&governed); err != nil {
		return nil, fmt.Errorf("asking whether a lock governs %s: %w", repository, err)
	}
	if governed == 0 {
		return []LockSelection{}, nil
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT node_key, owner, repo, path, commit_sha FROM (`+
		lockGovernedQuery(oneRepository)+`)
		ORDER BY node_key, commit_sha`, repository, owner, repo, analyzed)
	if err != nil {
		return nil, fmt.Errorf("reading lock selections for %s: %w", repository, err)
	}
	defer rows.Close()

	out := []LockSelection{}
	for rows.Next() {
		var sel LockSelection
		if err := rows.Scan(&sel.NodeKey, &sel.Owner, &sel.Repo, &sel.Path, &sel.Commit); err != nil {
			return nil, fmt.Errorf("reading lock selections for %s: %w", repository, err)
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

// LockGovernedEdges reports every edge a current lock governs, across the corpus.
//
// Nothing at all when no lock has been read, without walking anything: the corpus this was
// measured on holds no lock, and every reader asking pays for this.
func (s *Store) LockGovernedEdges() ([]LockGoverned, error) {
	var any bool
	if err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM abom_lock_dependency)`).Scan(&any); err != nil {
		return nil, fmt.Errorf("asking whether any lock was read: %w", err)
	}
	if !any {
		return []LockGoverned{}, nil
	}
	rows, err := s.db.Query(lockGovernedQuery(everyRepository) + `
		ORDER BY node_key, declared_ref, commit_sha, repository, consumer, context`)
	if err != nil {
		return nil, fmt.Errorf("reading what locks govern: %w", err)
	}
	defer rows.Close()

	out := []LockGoverned{}
	for rows.Next() {
		var g LockGoverned
		if err := rows.Scan(&g.EdgeID, &g.EdgeKey, &g.Repository, &g.NodeKey, &g.Owner, &g.Repo, &g.Path,
			&g.DeclaredRef, &g.Commit, &g.Consumer, &g.ConsumerCommit, &g.Context,
			&g.Rule); err != nil {
			return nil, fmt.Errorf("reading what locks govern: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// lockScope is which edges the governed relation is asked about.
type lockScope int

const (
	// oneRepository takes the repository, its owner, its name and the commit being settled.
	oneRepository lockScope = iota
	// everyRepository takes nothing.
	everyRepository
)

// lockGovernedQuery is the one definition of which lock governs which edge. Discovery asks it
// about one repository and the readers about all of them; two definitions would disagree about
// what runs exactly where a reader is meant to explain what discovery queued.
//
// Every lock table is reached by its key, never scanned: the tables grow with every locked commit
// the corpus holds.
func lockGovernedQuery(scope lockScope) string {
	settling, scoped := "", ""
	switch scope {
	case oneRepository:
		// The commit being settled is current before the queue records it.
		settling = `SELECT LOWER(?1), LOWER(?4) WHERE ?4 <> ''
	  UNION
	  `
		scoped = `SELECT r.row_id, r.repository_full_name, LOWER(?2), LOWER(?3)
	  FROM repository r WHERE r.repository_full_name = ?1`
	case everyRepository:
		// A repository a lock is about: one that holds a lock, or one a lock's entry names.
		scoped = `SELECT r.row_id, r.repository_full_name,
	         LOWER(SUBSTR(r.repository_full_name, 1, INSTR(r.repository_full_name, '/') - 1)),
	         LOWER(SUBSTR(r.repository_full_name, INSTR(r.repository_full_name, '/') + 1))
	  FROM repository r
	  WHERE INSTR(r.repository_full_name, '/') > 0
	    AND (EXISTS (SELECT 1 FROM abom_lock_workflow_pin w WHERE w.repository = r.repository_full_name COLLATE NOCASE)
	         OR EXISTS (SELECT 1 FROM abom_lock_dependency d
	                    WHERE d.owner = LOWER(SUBSTR(r.repository_full_name, 1, INSTR(r.repository_full_name, '/') - 1))
	                      AND d.repo = LOWER(SUBSTR(r.repository_full_name, INSTR(r.repository_full_name, '/') + 1))))`
	}
	return `
	WITH RECURSIVE current_root(repository, commit_sha) AS (
	  ` + settling + currentRoots + `
	),
	scoped(row_id, full_name, owner, repo) AS (
	  ` + scoped + `
	),
	-- The repository's own composites each of its workflows reaches, through any depth of
	-- composites calling composites. Seeded only where a lock is there to govern them.
	reach(row_id, full_name, workflow_path, node_row_id) AS (
	  SELECT sc.row_id, sc.full_name, e.file_path, e.dst_node_row_id
	  FROM scoped sc
	  CROSS JOIN abom_edge_regular e ON e.repository_row_id = sc.row_id
	  CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  WHERE e.edge_kind = 'step-uses-action' AND n.is_external = 0
	    AND EXISTS (SELECT 1 FROM abom_lock_workflow_pin w WHERE w.repository = sc.full_name COLLATE NOCASE)
	  UNION
	  -- CROSS JOIN fixes the order: outward from what is reached, never inward from every
	  -- internal node the corpus holds.
	  SELECT reach.row_id, reach.full_name, reach.workflow_path, e.dst_node_row_id
	  FROM reach
	  CROSS JOIN abom_node_live s ON s.container_node_row_id = reach.node_row_id
	  CROSS JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
	  CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  WHERE e.edge_kind = 'composite-step-uses-action' AND n.is_external = 0
	)
	SELECT e.row_id AS edge_row_id, e.edge_key AS edge_key, sc.full_name AS repository,
	       n.node_key AS node_key,
	       n.owner AS owner, n.repo AS repo, COALESCE(n.path, '') AS path,
	       COALESCE(e.ref_value, '') AS declared_ref, d.digest AS commit_sha,
	       w.repository AS consumer, w.commit_sha AS consumer_commit,
	       w.workflow_path AS context, '` + LockRuleWorkflow + `' AS rule
	FROM scoped sc
	CROSS JOIN abom_edge_regular e ON e.repository_row_id = sc.row_id
	CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	JOIN abom_lock_workflow_pin w ON w.repository = sc.full_name COLLATE NOCASE AND w.workflow_path = e.file_path
	JOIN current_root c ON c.repository = LOWER(w.repository) AND c.commit_sha = LOWER(w.commit_sha)
	JOIN abom_lock_dependency d
	  ON d.repository = w.repository AND d.commit_sha = w.commit_sha AND d.pin_key = w.pin_key
	WHERE e.edge_kind = 'step-uses-action' AND ` + lockEntryNamesTarget + `
	UNION
	SELECT e.row_id, e.edge_key, reach.full_name, n.node_key, n.owner, n.repo, COALESCE(n.path, ''),
	       COALESCE(e.ref_value, ''), d.digest, w.repository, w.commit_sha, reach.workflow_path,
	       '` + LockRuleOwnComposite + `'
	FROM reach
	CROSS JOIN abom_node_live s ON s.container_node_row_id = reach.node_row_id
	CROSS JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
	CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	JOIN abom_lock_workflow_pin w
	  ON w.repository = reach.full_name COLLATE NOCASE AND w.workflow_path = reach.workflow_path
	JOIN current_root c ON c.repository = LOWER(w.repository) AND c.commit_sha = LOWER(w.commit_sha)
	JOIN abom_lock_dependency d
	  ON d.repository = w.repository AND d.commit_sha = w.commit_sha AND d.pin_key = w.pin_key
	WHERE e.edge_kind = 'composite-step-uses-action' AND ` + lockEntryNamesTarget + `
	UNION
	SELECT e.row_id, e.edge_key, sc.full_name, n.node_key, n.owner, n.repo, COALESCE(n.path, ''),
	       COALESCE(e.ref_value, ''), d.digest, parent.repository, parent.commit_sha,
	       parent.pin_key, '` + LockRuleCaller + `'
	FROM scoped sc
	JOIN abom_lock_dependency parent ON parent.owner = sc.owner AND parent.repo = sc.repo
	JOIN current_root c
	  ON c.repository = LOWER(parent.repository) AND c.commit_sha = LOWER(parent.commit_sha)
	JOIN abom_lock_use u
	  ON u.repository = parent.repository AND u.commit_sha = parent.commit_sha
	 AND u.parent_pin = parent.pin_key
	JOIN abom_lock_dependency d
	  ON d.repository = u.repository AND d.commit_sha = u.commit_sha AND d.pin_key = u.child_pin
	CROSS JOIN abom_edge_regular e ON e.repository_row_id = sc.row_id
	CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	WHERE e.edge_kind = 'composite-step-uses-action' AND ` + lockEntryNamesTarget
}

// currentRoots are the commits each repository currently resolves to, lower-cased, which is what
// makes a lock read at one of them the lock that describes it.
const currentRoots = `SELECT DISTINCT LOWER(f.owner || '/' || f.repo), LOWER(f.resolved_sha)
	  FROM abom_frontier f
	  WHERE f.owner IS NOT NULL AND f.repo IS NOT NULL
	    AND COALESCE(f.resolved_sha, '') <> ''`

// lockEntryNamesTarget holds when lock entry d names the edge's external target at the ref the
// edge declares, and selects a SHA-1 commit other than that ref.
const lockEntryNamesTarget = `n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
	  AND d.owner = LOWER(n.owner) AND d.repo = LOWER(n.repo)
	  AND d.ref = COALESCE(e.ref_value, '')
	  AND d.digest_algorithm = 'sha1' AND d.digest <> ''
	  AND LOWER(COALESCE(e.ref_value, '')) <> d.digest`
