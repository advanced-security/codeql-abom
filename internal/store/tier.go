package store

import "fmt"

// Dependency tiers.
//
// Direct and transitive are derived from depth rather than stored, because depth is already
// maintained as the shortest route from the repositories under review and a second copy could
// disagree with it.
//
// The distinction is about who can act: a direct dependency is named by a repository under
// review and can be changed there, while a transitive one arrives through an intermediary that
// may belong to someone else. Source repositories are not in this queue at all — they are what
// the queue is measured from.
//
// Owned is the exception, and deliberately not a depth range: a dependency the operator owns is
// reached at whatever distance the graph happens to place it, and owning it is a different answer
// to "what can you do about this" than naming it. It therefore takes precedence, which also keeps
// the three exclusive — a dependency counted twice would make the tiers exceed the queue.
const (
	TierOwned      = "owned"
	TierDirect     = "direct"
	TierTransitive = "transitive"
)

// isOwned matches a queued dependency against the declared scope — the accounts the operator has
// stated they hold both the privilege to analyze and the responsibility to remediate.
var isOwned = ownedFrontierRow

// tierOfRow is the precedence, written once. It feeds both the counts and the requeue, which
// would otherwise be free to disagree about what a tier contains.
var tierOfRow = `CASE WHEN ` + isOwned + ` THEN '` + TierOwned + `'
	                    WHEN depth <= 1 THEN '` + TierDirect + `'
	                    ELSE '` + TierTransitive + `' END`

// tierWhere renders the membership of a tier.
func tierWhere(tier string) (string, error) {
	switch tier {
	case TierOwned, TierDirect, TierTransitive:
		return tierOfRow + " = '" + tier + "'", nil
	default:
		return "", fmt.Errorf("store: %q is not a dependency tier; expected %q, %q or %q",
			tier, TierOwned, TierDirect, TierTransitive)
	}
}

// RequeueTier puts an entire tier back in the queue, returning how many were requeued.
//
// Nothing already known is cleared. The previous analysis stays on record until a new one
// replaces it, so re-processing does not blank the Explorer for as long as the re-run takes.
//
// No attempt is charged: nothing failed, the work is being asked for again, and charging it
// would let a few re-runs exhaust a dependency's retry budget.
func (s *Store) RequeueTier(tier string) (int, error) {
	where, err := tierWhere(tier)
	if err != nil {
		return 0, err
	}
	// Unavailable is left alone: re-running a checkout that cannot succeed is the one outcome
	// already known in advance.
	res, err := s.db.Exec(`
		UPDATE abom_frontier
		SET status = 'pending', attempts = 0, next_attempt_at = NULL, claimed_at = NULL
		WHERE status = 'done' AND ` + where)
	if err != nil {
		return 0, fmt.Errorf("requeueing the %s tier: %w", tier, err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// TierCount separates how big a tier is from how much of it is currently analyzed.
//
// They are different questions and were one number for too long: binding a suite to a tier costs
// according to the tier's size, while re-analyzing one costs according to what is on record. A
// requeue empties Analyzed and leaves Total alone, so a tier being re-analyzed must never read as
// a tier holding nothing.
type TierCount struct {
	Total    int `json:"total"`
	Analyzed int `json:"analyzed"`
	// Repositories are the operator's own repositories in the queue, which enumeration writes at
	// depth 0, counted once each however many branches they are queued at. Analyzed means analyzed
	// at the default branch, the one every reader of a repository's results reads.
	Repositories         int `json:"repositories"`
	RepositoriesAnalyzed int `json:"repositoriesAnalyzed"`
	// Branches are those repositories' protected branches, each its own depth-0 row and its own
	// analysis, counted apart so a repository is never counted once per branch.
	Branches         int `json:"branches"`
	BranchesAnalyzed int `json:"branchesAnalyzed"`
	// Dependencies are the tier's rows that are somebody's dependency rather than one of the
	// operator's repositories. Counted from the rows, so the parts reconcile with Total.
	Dependencies         int `json:"dependencies"`
	DependenciesAnalyzed int `json:"dependenciesAnalyzed"`
	// NothingToAnalyze counts rows read in full with nothing to analyze, which are neither analyzed
	// nor outstanding; RepositoriesNothingToAnalyze those that are the operator's own default
	// branches, counted once each like RepositoriesAnalyzed.
	NothingToAnalyze             int `json:"nothingToAnalyze"`
	RepositoriesNothingToAnalyze int `json:"repositoriesNothingToAnalyze"`
}

// TierCounts reports how much work each tier holds, so the cost of re-processing one is stated
// before it is spent rather than discovered afterwards.
func (s *Store) TierCounts() (map[string]TierCount, error) {
	out := map[string]TierCount{
		TierOwned: {}, TierDirect: {}, TierTransitive: {},
	}
	rows, err := s.db.Query(`
		SELECT ` + tierOfRow + `, COUNT(*),
		       SUM(status = 'done'),
		       COUNT(DISTINCT CASE WHEN depth = 0 THEN node_key END),
		       COUNT(DISTINCT CASE WHEN depth = 0 AND ref = '' AND status = 'done'
		                           THEN node_key END),
		       SUM(depth = 0 AND ref <> ''),
		       SUM(depth = 0 AND ref <> '' AND status = 'done'),
		       SUM(depth <> 0),
		       SUM(depth <> 0 AND status = 'done'),
		       SUM(status = 'nothing'),
		       COUNT(DISTINCT CASE WHEN depth = 0 AND ref = '' AND status = 'nothing'
		                           THEN node_key END)
		FROM abom_frontier GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("counting tiers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier string
		var c TierCount
		if err := rows.Scan(&tier, &c.Total, &c.Analyzed,
			&c.Repositories, &c.RepositoriesAnalyzed, &c.Branches, &c.BranchesAnalyzed,
			&c.Dependencies, &c.DependenciesAnalyzed, &c.NothingToAnalyze,
			&c.RepositoriesNothingToAnalyze); err != nil {
			return nil, err
		}
		out[tier] = c
	}
	return out, rows.Err()
}
