package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// OwnedOrgs is kept apart from every derived notion of ownership on purpose. An account holding
// a seed repository is in scope, which says the corpus contains something of theirs; it says
// nothing about whether anyone here can change it.
func (s *Store) OwnedOrgs() ([]string, error) {
	return queryAll(s, "reading owned orgs", func(r *sql.Rows) (string, error) {
		var owner string
		return owner, r.Scan(&owner)
	}, `SELECT owner FROM abom_owned_org ORDER BY owner`)
}

// SetOwnedOrgs replaces the declaration wholesale.
func (s *Store) SetOwnedOrgs(owners []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("declaring owned orgs: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_owned_org`); err != nil {
		return fmt.Errorf("clearing owned orgs: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	seen := map[string]bool{}
	for _, owner := range owners {
		owner = strings.TrimSpace(owner)
		// GitHub treats account names case-insensitively, so a differently-cased duplicate is
		// the same declaration and must not become a second row.
		if owner == "" || seen[strings.ToLower(owner)] {
			continue
		}
		seen[strings.ToLower(owner)] = true
		if _, err := tx.Exec(
			`INSERT INTO abom_owned_org (owner, added_at) VALUES (?, ?)`, owner, now); err != nil {
			return fmt.Errorf("declaring owned org %s: %w", owner, err)
		}
	}
	return tx.Commit()
}

// OwnedSet lower-cases its keys, because GitHub treats account names case-insensitively and so
// must membership.
func (s *Store) OwnedSet() (map[string]bool, error) {
	owners, err := s.OwnedOrgs()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(owners))
	for _, o := range owners {
		set[strings.ToLower(o)] = true
	}
	return set, nil
}

// OwnerCandidate is an account that could plausibly be declared, with the weight behind it.
type OwnerCandidate struct {
	Owner string `json:"owner"`
	// Repositories counts only repositories in scope: an account known solely as a dependency
	// is not one the operator is likely to control.
	Repositories int  `json:"repositories"`
	Findings     int  `json:"findings"`
	Owned        bool `json:"owned"`
}

// OwnerCandidates is deliberately a suggestion. The actions account appears here because it
// holds a seed repository, which is precisely why the declaration is a choice and not a
// derivation.
func (s *Store) OwnerCandidates() ([]OwnerCandidate, error) {
	// Read before opening the cursor below: one connection serves this store, so a query issued
	// while another's rows are still open waits for a connection that only closing them frees.
	owned, err := s.OwnedSet()
	if err != nil {
		return nil, err
	}

	out, err := queryAll(s, "reading owner candidates", func(r *sql.Rows) (OwnerCandidate, error) {
		var c OwnerCandidate
		if err := r.Scan(&c.Owner, &c.Repositories, &c.Findings); err != nil {
			return c, err
		}
		c.Owned = owned[strings.ToLower(c.Owner)]
		return c, nil
	}, `
		SELECT SUBSTR(rep.repository_full_name, 1,
		              INSTR(rep.repository_full_name, '/') - 1) AS owner,
		       COUNT(DISTINCT rep.row_id),
		       COALESCE(SUM((SELECT COUNT(*) FROM abom_finding f
		                      WHERE f.repository_row_id = rep.row_id)), 0)
		FROM repository rep
		WHERE rep.origin = ?
		GROUP BY owner
		ORDER BY COUNT(DISTINCT rep.row_id) DESC, owner`, OriginSeed)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Owned != out[j].Owned {
			return out[i].Owned
		}
		return out[i].Repositories > out[j].Repositories
	})
	return out, nil
}

// OwnedSplit is a figure divided into the part the operator can act on and the part they cannot.
//
// Both halves are always present. Reporting only the actionable half would understate what the
// corpus actually contains.
type OwnedSplit struct {
	Yours     int `json:"yours"`
	Elsewhere int `json:"elsewhere"`
}

// Declared reports whether any account has been declared at all. With none, every figure is
// undivided and the interface must read exactly as it did before the feature existed.
func (s *Store) Declared() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_owned_org`).Scan(&n)
	return n > 0, err
}
