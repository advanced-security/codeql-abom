package store

import "fmt"

// nodeUsersBudget bounds one answer. The corpus's widest version is named by 285 repositories,
// so this is headroom rather than a working limit — but a list that is cut has to say so, which
// is why the budget travels with a Truncated flag instead of being assumed generous.
const nodeUsersBudget = 2000

// RepositoryUse is one repository naming a node at one reference.
type RepositoryUse struct {
	FullName string `json:"fullName"`
	// Origin separates a repository under review from one fetched only to resolve what those
	// depend on. Both run the version; only the first is anyone's to change here.
	Origin string `json:"origin"`
	Owned  bool   `json:"owned"`
	// Uses counts the `uses:` lines, which is how much editing the repository would be. One
	// repository naming a version from three workflows is one row and three places.
	Uses int `json:"uses"`
}

// NodeUsers is every repository naming one node at one exact reference.
//
// Direct only, by construction: these are edges that name the node themselves. Anything reaching
// it through an intermediary is `BumpImpactOf`, and the two must not be added together.
type NodeUsers struct {
	Key   string `json:"key"`
	Ref   string `json:"ref"`
	Total int    `json:"total"`
	Seeds int    `json:"seeds"`
	Owned int    `json:"owned"`
	// OwnedKnown is false when no account has been verified. Without it, "0 yours" reads as a
	// count of none rather than as a question nobody has answered.
	OwnedKnown bool            `json:"ownedKnown"`
	Repos      []RepositoryUse `json:"repos"`
	Truncated  bool            `json:"truncated"`
}

// NodeUsersOf lists every repository naming a node at one reference.
func (s *Store) NodeUsersOf(key, ref string) (NodeUsers, error) {
	return s.nodeUsersOf(key, ref, nodeUsersBudget)
}

func (s *Store) nodeUsersOf(key, ref string, budget int) (NodeUsers, error) {
	out := NodeUsers{Key: key, Ref: ref, Repos: []RepositoryUse{}}

	verified, err := s.Verified()
	if err != nil {
		return out, err
	}
	out.OwnedKnown = verified

	if err := s.readNodeUsers(&out, budget); err != nil {
		return out, err
	}
	if err := s.countNodeUsers(&out); err != nil {
		return out, err
	}
	return out, nil
}

// readNodeUsers fills the rows, in its own function so `rows` is closed before anything else
// queries. The pool holds one connection, so a loop left open by a `break` deadlocks the next
// statement rather than failing — and only on the path where the budget bites.
func (s *Store) readNodeUsers(out *NodeUsers, budget int) error {
	// One row per repository, not per edge: the count of places to edit and the count of
	// repositories are different numbers and the interface needs both.
	rows, err := s.db.Query(`
		SELECT r.repository_full_name, COALESCE(r.origin, ''), COUNT(*),
		       EXISTS (SELECT 1 FROM abom_account a
		                WHERE LOWER(a.owner) = LOWER(SUBSTR(r.repository_full_name, 1,
		                      INSTR(r.repository_full_name, '/') - 1)))
		FROM abom_edge e
		JOIN abom_node n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE n.node_key = ? AND COALESCE(e.ref_value, '') = ?
		GROUP BY r.repository_full_name, r.origin
		ORDER BY COUNT(*) DESC, r.repository_full_name
		LIMIT ?`, out.Key, out.Ref, budget+1)
	if err != nil {
		return fmt.Errorf("reading users of %q at %q: %w", out.Key, out.Ref, err)
	}
	defer rows.Close()

	for rows.Next() {
		var u RepositoryUse
		if err := rows.Scan(&u.FullName, &u.Origin, &u.Uses, &u.Owned); err != nil {
			return fmt.Errorf("scanning a user of %q: %w", out.Key, err)
		}
		// One past the budget is how the cut is detected without a second count.
		if len(out.Repos) == budget {
			out.Truncated = true
			break
		}
		out.Repos = append(out.Repos, u)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading users of %q: %w", out.Key, err)
	}
	return nil
}

// countNodeUsers totals the whole set, which is not len(Repos) once a budget has bitten.
func (s *Store) countNodeUsers(out *NodeUsers) error {
	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(seed), 0), COALESCE(SUM(owned), 0) FROM (
			SELECT r.origin = ? AS seed,
			       EXISTS (SELECT 1 FROM abom_account a
			                WHERE LOWER(a.owner) = LOWER(SUBSTR(r.repository_full_name, 1,
			                      INSTR(r.repository_full_name, '/') - 1))) AS owned
			FROM abom_edge e
			JOIN abom_node n ON n.row_id = e.dst_node_row_id
			JOIN repository r ON r.row_id = e.repository_row_id
			WHERE n.node_key = ? AND COALESCE(e.ref_value, '') = ?
			GROUP BY r.repository_full_name, r.origin)`,
		OriginSeed, out.Key, out.Ref).Scan(&out.Total, &out.Seeds, &out.Owned)
	if err != nil {
		return fmt.Errorf("counting users of %q: %w", out.Key, err)
	}
	return nil
}
