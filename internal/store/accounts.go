package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Account is an account the operator is verified to belong to.
type Account struct {
	Owner string `json:"owner"`
	// Kind is "user" for the operator's own account, "organization" otherwise.
	Kind string `json:"kind"`
	// Role is "owner" for their own account, and GitHub's "admin" or "member" for an org.
	Role string `json:"role"`
}

// The roles that carry enough access to answer for an account.
//
// Deliberately coarse: read-only membership is not enough to make an operator responsible for an
// account. Finer roles belong with scoped-token support.
const (
	RoleOwner = "owner"
	RoleAdmin = "admin"
)

// CanDeclare reports whether this role carries enough access to be responsible for the account.
func (a Account) CanDeclare() bool { return a.Role == RoleOwner || a.Role == RoleAdmin }

// MarshalJSON carries the derived eligibility to the interface rather than leaving it to read the
// role and reach its own conclusion, which is the second derivation this exists to prevent.
func (a Account) MarshalJSON() ([]byte, error) {
	type account Account // Prevents this method recursing through itself.
	return json.Marshal(struct {
		account
		CanDeclare bool `json:"canDeclare"`
	}{account(a), a.CanDeclare()})
}

// Verification is a completed identity check and the accounts it established.
//
// Login, Host and VerifiedAt distinguish verified accounts from manually declared ones.
type Verification struct {
	Login      string    `json:"login"`
	Host       string    `json:"host"`
	VerifiedAt time.Time `json:"verifiedAt"`
	Accounts   []Account `json:"accounts"`
	// Scopes the token carried. Empty means nobody asked, which is not the same as a token that
	// carries none: both are recorded as "cannot say", because guessing decides whether an
	// operator is locked out of their own bill of materials.
	Scopes []string `json:"scopes"`
}

// CanReadMemberships reports whether the token could see the whole organization list.
//
// `user/memberships/orgs` returns what you belong to and never what you do not, so an account
// absent from it is evidence against the operator **only** if the list was complete. A classic
// token missing `read:org` is refused outright, but a fine-grained or App token reports no
// classic scopes at all and may answer with only the accounts it was issued for — a partial list
// that looks exactly like a complete one. Treating that as complete would contradict accounts the
// operator really does own.
func (v Verification) CanReadMemberships() bool {
	for _, s := range v.Scopes {
		if s == "read:org" || s == "admin:org" {
			return true
		}
	}
	return false
}

// ReplaceAccounts records a verification wholesale, discarding whatever preceded it.
//
// Replacement rather than merge: losing access to an organization has to remove it.
func (s *Store) ReplaceAccounts(v Verification) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording verified accounts: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_account`); err != nil {
		return fmt.Errorf("clearing verified accounts: %w", err)
	}
	at := v.VerifiedAt.UTC().Format(time.RFC3339)
	// NULL when nobody asked, so a later read can tell an unanswered question from a token that
	// genuinely carried no scopes.
	var scopes any
	if len(v.Scopes) > 0 {
		scopes = strings.Join(v.Scopes, ",")
	}
	seen := map[string]bool{}
	for _, a := range v.Accounts {
		owner := strings.TrimSpace(a.Owner)
		// GitHub treats account names case-insensitively, so a differently-cased repeat is the
		// same account and must not become a second row.
		if owner == "" || seen[strings.ToLower(owner)] {
			continue
		}
		seen[strings.ToLower(owner)] = true
		if _, err := tx.Exec(`
			INSERT INTO abom_account (owner, kind, role, host, verified_as, verified_at, scopes)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			owner, a.Kind, a.Role, v.Host, v.Login, at, scopes); err != nil {
			return fmt.Errorf("recording verified account %s: %w", owner, err)
		}
	}
	return tx.Commit()
}

// Accounts reads the stored verification, including what established it.
func (s *Store) Accounts() (Verification, error) {
	var v Verification
	accounts, err := queryAll(s, "reading verified accounts", func(r *sql.Rows) (Account, error) {
		var a Account
		var host, login, at string
		var scopes sql.NullString
		if err := r.Scan(&a.Owner, &a.Kind, &a.Role, &host, &login, &at, &scopes); err != nil {
			return a, err
		}
		// Every row carries the same verification, so the last one read settles the provenance.
		v.Host, v.Login = host, login
		v.VerifiedAt, _ = time.Parse(time.RFC3339, at)
		v.Scopes = nil
		if scopes.Valid && scopes.String != "" {
			v.Scopes = strings.Split(scopes.String, ",")
		}
		return a, nil
	}, `SELECT owner, kind, role, host, verified_as, verified_at, scopes
	      FROM abom_account ORDER BY kind DESC, owner`)
	if err != nil {
		return v, err
	}
	v.Accounts = accounts
	return v, nil
}

// ForgetAccounts discards the stored verification.
func (s *Store) ForgetAccounts() error {
	if _, err := s.db.Exec(`DELETE FROM abom_account`); err != nil {
		return fmt.Errorf("forgetting verified accounts: %w", err)
	}
	return nil
}

// OwnedSet is the declared accounts, lower-cased because GitHub treats account names
// case-insensitively and so must membership.
//
// Owner granularity: an account restricted by an include list is still the operator's account,
// and callers judging a whole *repository* use `RepositoryIsOwned`, which honors the list.
func (s *Store) OwnedSet() (map[string]bool, error) {
	owners, err := queryAll(s, "reading declared accounts", func(r *sql.Rows) (string, error) {
		var owner string
		return owner, r.Scan(&owner)
	}, `SELECT owner FROM abom_owned_scope_honored WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(owners))
	for _, o := range owners {
		set[strings.ToLower(o)] = true
	}
	return set, nil
}

// Verified reports whether an identity has been established at all.
func (s *Store) Verified() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_account`).Scan(&n)
	return n > 0, err
}

// OwnerCandidate is an account holding repositories in scope, and whether it is one of yours.
type OwnerCandidate struct {
	Owner string `json:"owner"`
	// Repositories counts seeded repositories only: an account known solely as a dependency is
	// not one the operator is likely to belong to. Seeded, not analyzed — an account freshly
	// enumerated holds all of them with nothing analyzed yet.
	Repositories int  `json:"repositories"`
	Findings     int  `json:"findings"`
	Owned        bool `json:"owned"`
}

// OwnerCandidates lists seeded repository owners, marking those verified as the operator's.
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
		       COALESCE(SUM((SELECT COUNT(*) FROM abom_finding_regular f
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
