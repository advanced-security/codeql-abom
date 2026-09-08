package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ownedScopeDDL is the fourth ownership notion, and the only one that means responsibility.
//
// `repository.origin` records how a repository entered the corpus. `abom_account` records what
// GitHub says the operator belongs to. Neither means anyone can change anything: `actions` owns a
// repository in scope and nobody running this tool controls it. This table is the operator's own
// statement about which accounts they hold both the privilege to analyze and the responsibility
// to remediate, and it is the one discovery is bounded by.
const ownedScopeDDL = `
CREATE TABLE IF NOT EXISTS abom_owned_scope (
  owner         TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  -- Held rather than derived from the include list being non-empty: removing the last include
  -- would otherwise widen an account from two repositories to all of them without anyone saying
  -- so. Set with an empty list, this scopes to nothing, which is the safe reading.
  includes_only INTEGER NOT NULL DEFAULT 0,
  declared_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS abom_owned_include (
  owner TEXT NOT NULL,
  repo  TEXT NOT NULL,
  PRIMARY KEY (owner, repo)
);`

// Account kinds. A user account is inherently one person's, which is why it is opted into.
const (
	AccountOrganization = "organization"
	AccountUser         = "user"
)

// DeclaredAccount is one account the operator has stated is theirs.
type DeclaredAccount struct {
	Owner string `json:"owner"`
	Kind  string `json:"kind"`
	// Enabled is false for a declaration that is recorded but not acted on. The operator's own
	// account is declared this way until they opt in.
	Enabled bool `json:"enabled"`
	// IncludesOnly restricts the account to Includes. See the DDL for why it is not derived.
	IncludesOnly bool     `json:"includesOnly"`
	Includes     []string `json:"includes"`
	DeclaredAt   string   `json:"declaredAt"`
}

// Covers reports whether a repository falls inside this account's declaration.
//
// The Go form of `ownedRepository`, for the one caller that cannot use SQL: a declaration being
// previewed has not been stored, and writing it in order to measure it would make canceling
// impossible. `TestCoveredByAgreesWithTheStoredScopePredicate` is what stops the two drifting.
func (a DeclaredAccount) Covers(owner, repo string) bool {
	if !a.Enabled || !strings.EqualFold(a.Owner, owner) {
		return false
	}
	if !a.IncludesOnly {
		return true
	}
	for _, include := range a.Includes {
		if strings.EqualFold(include, repo) {
			return true
		}
	}
	return false
}

// CoveredBy reports whether any account in a declaration covers a repository.
func CoveredBy(accounts []DeclaredAccount, owner, repo string) bool {
	for _, a := range accounts {
		if a.Covers(owner, repo) {
			return true
		}
	}
	return false
}

// ownedRepository matches a repository full name against the declared scope. Written once so the
// tier, the queue and every "yours" split cannot drift into separate answers.
//
// Both sides are lower-cased because GitHub treats account names case-insensitively.
const ownedRepository = `EXISTS (
	SELECT 1 FROM abom_owned_scope o
	 WHERE o.enabled = 1
	   AND LOWER(o.owner) = LOWER(?)
	   AND (o.includes_only = 0
	        OR EXISTS (SELECT 1 FROM abom_owned_include i
	                    WHERE LOWER(i.owner) = LOWER(?) AND LOWER(i.repo) = LOWER(?))))`

// ownedFrontierRow is the same predicate against a queue row, for use inside a larger statement.
const ownedFrontierRow = `EXISTS (
	SELECT 1 FROM abom_owned_scope o
	 WHERE o.enabled = 1
	   AND LOWER(o.owner) = LOWER(abom_frontier.owner)
	   AND (o.includes_only = 0
	        OR EXISTS (SELECT 1 FROM abom_owned_include i
	                    WHERE LOWER(i.owner) = LOWER(abom_frontier.owner)
	                      AND LOWER(i.repo) = LOWER(abom_frontier.repo))))`

// ownedFullName renders the predicate against a column holding `owner/name`. Built rather than
// written out three times, so the tier, the queue view and the node users cannot drift into
// separate answers to the same question.
func ownedFullName(col string) string {
	owner := `SUBSTR(` + col + `, 1, INSTR(` + col + `, '/') - 1)`
	repo := `SUBSTR(` + col + `, INSTR(` + col + `, '/') + 1)`
	return `EXISTS (
		SELECT 1 FROM abom_owned_scope o
		 WHERE o.enabled = 1
		   AND LOWER(o.owner) = LOWER(` + owner + `)
		   AND (o.includes_only = 0
		        OR EXISTS (SELECT 1 FROM abom_owned_include i
		                    WHERE LOWER(i.owner) = LOWER(` + owner + `)
		                      AND LOWER(i.repo) = LOWER(` + repo + `))))`
}

// SetOwnedScope records the declaration wholesale, discarding whatever preceded it.
//
// Replacement rather than merge, for the reason ReplaceAccounts gives: an account that accumulated
// would keep counting as the operator's long after they stopped being responsible for it.
func (s *Store) SetOwnedScope(accounts []DeclaredAccount) error {
	for _, a := range accounts {
		if strings.TrimSpace(a.Owner) == "" {
			return fmt.Errorf("store: a declared account needs an owner")
		}
		if a.Kind != AccountOrganization && a.Kind != AccountUser {
			return fmt.Errorf("store: %q is not an account kind; expected %q or %q",
				a.Kind, AccountOrganization, AccountUser)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("declaring owned scope: %w", err)
	}
	defer tx.Rollback()

	for _, stmt := range []string{`DELETE FROM abom_owned_include`, `DELETE FROM abom_owned_scope`} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("clearing the declared scope: %w", err)
		}
	}

	at := now()
	seen := map[string]bool{}
	for _, a := range accounts {
		owner := strings.TrimSpace(a.Owner)
		if seen[strings.ToLower(owner)] {
			continue
		}
		seen[strings.ToLower(owner)] = true
		if _, err := tx.Exec(`
			INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_at)
			VALUES (?, ?, ?, ?, ?)`,
			owner, a.Kind, a.Enabled, a.IncludesOnly, at); err != nil {
			return fmt.Errorf("declaring %s: %w", owner, err)
		}
		for _, repo := range a.Includes {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				continue
			}
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO abom_owned_include (owner, repo) VALUES (?, ?)`,
				owner, repo); err != nil {
				return fmt.Errorf("including %s/%s: %w", owner, repo, err)
			}
		}
	}
	return tx.Commit()
}

// OwnedScope reads the declaration, each account carrying the repositories it is restricted to.
func (s *Store) OwnedScope() ([]DeclaredAccount, error) {
	accounts, err := queryAll(s, "reading the declared scope", func(r *sql.Rows) (DeclaredAccount, error) {
		var a DeclaredAccount
		a.Includes = []string{}
		return a, r.Scan(&a.Owner, &a.Kind, &a.Enabled, &a.IncludesOnly, &a.DeclaredAt)
	}, `SELECT owner, kind, enabled, includes_only, declared_at
	      FROM abom_owned_scope ORDER BY kind DESC, owner`)
	if err != nil {
		return nil, err
	}

	includes, err := s.ownedIncludes()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if got, ok := includes[strings.ToLower(accounts[i].Owner)]; ok {
			accounts[i].Includes = got
		}
	}
	return accounts, nil
}

func (s *Store) ownedIncludes() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT owner, repo FROM abom_owned_include ORDER BY owner, repo`)
	if err != nil {
		return nil, fmt.Errorf("reading declared includes: %w", err)
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var owner, repo string
		if err := rows.Scan(&owner, &repo); err != nil {
			return nil, fmt.Errorf("scanning a declared include: %w", err)
		}
		key := strings.ToLower(owner)
		out[key] = append(out[key], repo)
	}
	return out, rows.Err()
}

// OwnedScopeDeclared reports whether anything is declared and enabled, which is the only question
// the discovery gate asks. Nothing declared and everything disabled are the same answer.
func (s *Store) OwnedScopeDeclared() (bool, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_owned_scope WHERE enabled = 1`).Scan(&n); err != nil {
		return false, fmt.Errorf("reading whether a scope is declared: %w", err)
	}
	return n > 0, nil
}

// OwnedRepositoryFilter is the declared-scope predicate against the `repository` table, for use
// inside a larger statement that has already joined it as `r`.
var OwnedRepositoryFilter = ownedFullName("r.repository_full_name")

// AllRepositories names every repository the corpus holds, in scope or not.
func (s *Store) AllRepositories() ([]string, error) {
	return queryAll(s, "reading repositories", func(r *sql.Rows) (string, error) {
		var name string
		return name, r.Scan(&name)
	}, `SELECT repository_full_name FROM repository ORDER BY repository_full_name`)
}

// OwnedRepositories lists every repository in the corpus that falls inside the declared scope.
//
// The graph is the only place repositories are known, so an account declared but never seen
// contributes nothing here — which is honest: it is a statement of intent that the corpus cannot
// yet act on.
func (s *Store) OwnedRepositories() ([]string, error) {
	return queryAll(s, "reading owned repositories", func(r *sql.Rows) (string, error) {
		var name string
		return name, r.Scan(&name)
	}, `SELECT repository_full_name FROM repository
	     WHERE `+ownedFullName("repository_full_name")+`
	     ORDER BY repository_full_name`)
}

// RepositoryIsOwned reports whether one `owner/name` falls inside the declared scope.
func (s *Store) RepositoryIsOwned(fullName string) (bool, error) {
	owner, repo, ok := splitRepository(fullName)
	if !ok {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 WHERE `+ownedRepository, owner, owner, repo).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading ownership of %q: %w", fullName, err)
	}
	return true, nil
}

// splitRepository refuses anything that does not name both halves. The junk `/` repository this
// corpus once held is exactly the shape that would otherwise pass as an empty owner.
func splitRepository(fullName string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(fullName, "/")
	if !found || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}
