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
// stated they hold both the privilege to analyze and the responsibility to remediate. It read the
// `gh`-verified accounts until 2026-09-07, which reported membership: being in an organization
// does not make its supply chain anyone's work.
const isOwned = ownedFrontierRow

// tierOfRow is the precedence, written once. It feeds both the counts and the requeue, which
// would otherwise be free to disagree about what a tier contains.
const tierOfRow = `CASE WHEN ` + isOwned + ` THEN '` + TierOwned + `'
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
	// Repositories are queue rows for the operator's own repositories, which enumeration writes
	// at depth 0. They joined the owned tier when enumeration started queueing them, and one
	// number for both hid that on the reference corpus all 91 analyzed rows were repositories
	// and not one of the 44 owned dependencies had been looked at.
	Repositories         int `json:"repositories"`
	RepositoriesAnalyzed int `json:"repositoriesAnalyzed"`
}

// Dependencies is the tier's work that is somebody's dependency rather than the operator's own
// repository. Derived so the two halves cannot drift out of agreement with the total.
func (c TierCount) Dependencies() int { return c.Total - c.Repositories }

// DependenciesAnalyzed counts the analyzed half of Dependencies.
func (c TierCount) DependenciesAnalyzed() int { return c.Analyzed - c.RepositoriesAnalyzed }

// TierCounts reports how much work each tier holds, so the cost of re-processing one is stated
// before it is spent rather than discovered afterwards.
func (s *Store) TierCounts() (map[string]TierCount, error) {
	out := map[string]TierCount{
		TierOwned: {}, TierDirect: {}, TierTransitive: {},
	}
	rows, err := s.db.Query(`
		SELECT ` + tierOfRow + `, COUNT(*),
		       SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN depth = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN depth = 0 AND status = 'done' THEN 1 ELSE 0 END)
		FROM abom_frontier GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("counting tiers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier string
		var c TierCount
		if err := rows.Scan(&tier, &c.Total, &c.Analyzed,
			&c.Repositories, &c.RepositoriesAnalyzed); err != nil {
			return nil, err
		}
		out[tier] = c
	}
	return out, rows.Err()
}
