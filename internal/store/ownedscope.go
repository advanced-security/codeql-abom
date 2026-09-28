package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ownedScopeDDL records the operator's responsibility boundary, distinct from origin or access.
const ownedScopeDDL = `
CREATE TABLE IF NOT EXISTS abom_owned_scope (
  owner         TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  -- Held rather than derived from the include list being non-empty: removing the last include
  -- would otherwise widen an account from two repositories to all of them without anyone saying
  -- so. Set with an empty list, this scopes to nothing, which is the safe reading.
  includes_only INTEGER NOT NULL DEFAULT 0,
  -- How the row arrived, decided by the code path that wrote it and never by its payload.
  -- NULL means nobody knows, which is the honest answer for every row written before this
  -- existed. See plans/db/declaration-provenance.md.
  declared_by   TEXT,
  declared_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS abom_owned_include (
  owner TEXT NOT NULL,
  repo  TEXT NOT NULL,
  PRIMARY KEY (owner, repo)
);`

// sawWholeMembershipList is the SQL spelling of Verification.CanReadMemberships: the token
// carried a scope that lets `user/memberships/orgs` return every organization, so a name absent
// from the answer is absent from the operator's memberships rather than from the token's view.
//
// The comma padding makes this an element match rather than a substring one, so a scope merely
// containing `read:org` does not qualify. `verified_as` guards the case the Go side spells as an
// empty login: rows with no verification behind them establish nothing.
// `TestTheGateAgreesWithTheDeclarationState` is what stops the two spellings drifting.
const sawWholeMembershipList = `EXISTS (
	SELECT 1 FROM abom_account a
	 WHERE a.verified_as <> ''
	   AND (',' || COALESCE(a.scopes, '') || ',' LIKE '%,read:org,%'
	        OR ',' || COALESCE(a.scopes, '') || ',' LIKE '%,admin:org,%'))`

// honoredScopeDDL centralizes the membership gate for every ownership read.
// Only contradicted declarations are held back; incomplete evidence leaves them unproven.
const honoredScopeDDL = `
CREATE VIEW IF NOT EXISTS abom_owned_scope_honored AS
SELECT * FROM abom_owned_scope o
 WHERE NOT (` + sawWholeMembershipList + `
            AND NOT EXISTS (SELECT 1 FROM abom_account b
                             WHERE LOWER(b.owner) = LOWER(o.owner)));`

// Account kinds. A user account is inherently one person's, which is why it is opted into.
const (
	AccountOrganization = "organization"
	AccountUser         = "user"
)

// How a declaration arrived. Set by the write path, never by the caller's payload: a client that
// could name its own source could claim any of them.
const (
	DeclaredByCLI       = "cli"
	DeclaredByInterface = "interface"
	// DeclaredByConfig is an untracked local file, read at startup. It is a channel, not an
	// authority: a row it writes is verified exactly like one that was typed.
	DeclaredByConfig = "config"
)

// What `gh` has to say about a declaration.
const (
	DeclarationUnknown      = "unknown"
	DeclarationConfirmed    = "confirmed"
	DeclarationContradicted = "contradicted"
)

// DeclarationState reports how a verification bears on a declaration.
//
// Contradicted means a complete membership list omitted the declared account.
func DeclarationState(a DeclaredAccount, v Verification) string {
	if v.Login == "" {
		return DeclarationUnknown
	}
	for _, known := range v.Accounts {
		if strings.EqualFold(known.Owner, a.Owner) {
			return DeclarationConfirmed
		}
	}
	// Absent from an incomplete list is not evidence of anything. Only a token that could have
	// seen the whole list may turn absence into a contradiction.
	if !v.CanReadMemberships() {
		return DeclarationUnknown
	}
	return DeclarationContradicted
}

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
	// DeclaredBy is output only: the store sets it from the write path and ignores whatever a
	// caller sends. Empty means the row predates the column and nobody knows who wrote it.
	DeclaredBy string `json:"declaredBy"`
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
	SELECT 1 FROM abom_owned_scope_honored o
	 WHERE o.enabled = 1
	   AND LOWER(o.owner) = LOWER(?)
	   AND (o.includes_only = 0
	        OR EXISTS (SELECT 1 FROM abom_owned_include i
	                    WHERE LOWER(i.owner) = LOWER(?) AND LOWER(i.repo) = LOWER(?))))`

// ownedFrontierRow is the same predicate against a queue row, for use inside a larger statement.
const ownedFrontierRow = `EXISTS (
	SELECT 1 FROM abom_owned_scope_honored o
	 WHERE o.enabled = 1
	   AND LOWER(o.owner) = LOWER(abom_frontier.owner)
	   AND (o.includes_only = 0
	        OR EXISTS (SELECT 1 FROM abom_owned_include i
	                    WHERE LOWER(i.owner) = LOWER(abom_frontier.owner)
	                      AND LOWER(i.repo) = LOWER(abom_frontier.repo))))
	AND NOT EXISTS (SELECT 1 FROM abom_repository_absence absent
	                 WHERE absent.repository = abom_frontier.owner || '/' || abom_frontier.repo)`

// ownedFullName renders the predicate against a column holding `owner/name`. Built rather than
// written out three times, so the tier, the queue view and the node users cannot drift into
// separate answers to the same question. A repository a full listing of its account no longer
// names is not the operator's, whatever the declaration says about the account.
func ownedFullName(col string) string {
	return `(` + declaredFullName(col) + ` AND NOT EXISTS (
		SELECT 1 FROM abom_repository_absence absent WHERE absent.repository = ` + col + `))`
}

// declaredFullName is what a declaration covers, missing or not, which is what a listing of the
// account is compared with.
func declaredFullName(col string) string {
	owner := `SUBSTR(` + col + `, 1, INSTR(` + col + `, '/') - 1)`
	repo := `SUBSTR(` + col + `, INSTR(` + col + `, '/') + 1)`
	return `EXISTS (
		SELECT 1 FROM abom_owned_scope_honored o
		 WHERE o.enabled = 1
		   AND LOWER(o.owner) = LOWER(` + owner + `)
		   AND (o.includes_only = 0
		        OR EXISTS (SELECT 1 FROM abom_owned_include i
		                    WHERE LOWER(i.owner) = LOWER(` + owner + `)
		                      AND LOWER(i.repo) = LOWER(` + repo + `))))`
}

// SetOwnedScope records the declaration wholesale, discarding whatever preceded it.
// `by` is the code path doing the writing, not anything the caller supplied.
func (s *Store) SetOwnedScope(accounts []DeclaredAccount, by string) error {
	if err := validateOwnedScope(accounts, by); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("declaring owned scope: %w", err)
	}
	defer tx.Rollback()

	if err := clearOwnedScope(tx); err != nil {
		return err
	}
	if err := insertOwnedScope(tx, accounts, by, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func validateOwnedScope(accounts []DeclaredAccount, by string) error {
	if by != DeclaredByCLI && by != DeclaredByInterface && by != DeclaredByConfig {
		return fmt.Errorf("store: %q is not a declaration source; expected %q, %q or %q",
			by, DeclaredByCLI, DeclaredByInterface, DeclaredByConfig)
	}
	for _, a := range accounts {
		if strings.TrimSpace(a.Owner) == "" {
			return fmt.Errorf("store: a declared account needs an owner")
		}
		if a.Kind != AccountOrganization && a.Kind != AccountUser {
			return fmt.Errorf("store: %q is not an account kind; expected %q or %q",
				a.Kind, AccountOrganization, AccountUser)
		}
	}
	return nil
}

func clearOwnedScope(tx *sql.Tx) error {
	for _, stmt := range []string{`DELETE FROM abom_owned_include`, `DELETE FROM abom_owned_scope`} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("clearing the declared scope: %w", err)
		}
	}
	return nil
}

func insertOwnedScope(tx *sql.Tx, accounts []DeclaredAccount, by, at string) error {
	seen := map[string]bool{}
	for _, a := range accounts {
		owner := strings.TrimSpace(a.Owner)
		key := strings.ToLower(owner)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := insertOwnedAccount(tx, owner, a, by, at); err != nil {
			return err
		}
	}
	return nil
}

func insertOwnedAccount(tx *sql.Tx, owner string, a DeclaredAccount, by, at string) error {
	if _, err := tx.Exec(`
		INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_by, declared_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		owner, a.Kind, a.Enabled, a.IncludesOnly, by, at); err != nil {
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
	return nil
}

// OwnedScope reads the declaration, each account carrying the repositories it is restricted to.
func (s *Store) OwnedScope() ([]DeclaredAccount, error) {
	accounts, err := queryAll(s, "reading the declared scope", func(r *sql.Rows) (DeclaredAccount, error) {
		var a DeclaredAccount
		var by sql.NullString
		a.Includes = []string{}
		err := r.Scan(&a.Owner, &a.Kind, &a.Enabled, &a.IncludesOnly, &by, &a.DeclaredAt)
		a.DeclaredBy = by.String
		return a, err
	}, `SELECT owner, kind, enabled, includes_only, declared_by, declared_at
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
// the discovery gate asks. Nothing declared and everything disabled are the same answer, and so
// is a scope whose every account `gh` answered against.
func (s *Store) OwnedScopeDeclared() (bool, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_owned_scope_honored WHERE enabled = 1`).Scan(&n); err != nil {
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
// A declared account contributes nothing here until it has been enumerated, which is honest: a
// declaration is a statement of intent, and enumeration is what turns it into repositories.
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
	err := s.db.QueryRow(`SELECT 1 WHERE `+ownedRepository+`
		AND NOT EXISTS (SELECT 1 FROM abom_repository_absence WHERE repository = ?)`,
		owner, owner, repo, fullName).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading ownership of %q: %w", fullName, err)
	}
	return true, nil
}

// splitRepository refuses anything that does not name both halves.
func splitRepository(fullName string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(fullName, "/")
	if !found || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}
