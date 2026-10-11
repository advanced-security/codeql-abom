package store

import (
	"database/sql"
	"fmt"
	"slices"
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
);
-- A property scope covers the repositories of a restricted account carrying every one of its
-- terms. Several on one account each count, which is how an operator says "this or that".
CREATE TABLE IF NOT EXISTS abom_owned_property_clause (
  id          INTEGER PRIMARY KEY,
  owner       TEXT NOT NULL COLLATE NOCASE,
  declared_by TEXT,
  declared_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_owned_property_clause_owner ON abom_owned_property_clause (owner);
CREATE TABLE IF NOT EXISTS abom_owned_property_term (
  clause_id     INTEGER NOT NULL,
  property_name TEXT NOT NULL COLLATE NOCASE,
  value         TEXT NOT NULL,
  PRIMARY KEY (clause_id, property_name, value)
);`

// propertyClauseViewDDL holds back a property scope the schema cannot vouch for: one naming a
// property GitHub has not described, one repository administrators can set, or one repeating a key
// that holds a single value. Recreated on every Migrate so a changed rule reaches every database;
// `holdReason` explains each case and `TestAHeldScopeIsHeldInSQLExactlyWhenItHasAReason` holds the
// two together.
const propertyClauseViewDDL = `
DROP VIEW IF EXISTS abom_owned_property_clause_honored;
CREATE VIEW abom_owned_property_clause_honored AS
SELECT c.* FROM abom_owned_property_clause c
 WHERE EXISTS (SELECT 1 FROM abom_owned_property_term t WHERE t.clause_id = c.id)
   AND NOT EXISTS (
     SELECT 1 FROM abom_owned_property_term t
      WHERE t.clause_id = c.id
        AND NOT EXISTS (
          SELECT 1 FROM abom_property_schema s
           WHERE s.owner = c.owner AND s.property_name = t.property_name
             AND s.values_editable_by = '` + PropertyEditableByOrganization + `'
             AND (s.value_type = 'multi_select'
                  OR NOT EXISTS (SELECT 1 FROM abom_owned_property_term u
                                  WHERE u.clause_id = t.clause_id
                                    AND u.property_name = t.property_name
                                    AND u.value <> t.value))));`

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

// PropertyTerm is one custom property value a repository must carry.
type PropertyTerm struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PropertyClause covers the repositories of its account that carry every one of its terms.
type PropertyClause struct {
	Terms []PropertyTerm `json:"terms"`
	// DeclaredBy and DeclaredAt are output only, set from the write path like an account's.
	DeclaredBy string `json:"declaredBy"`
	DeclaredAt string `json:"declaredAt"`
	// Held is read from the schema, never sent: a held scope is kept and counts nothing.
	Held       bool   `json:"held"`
	HeldReason string `json:"heldReason,omitempty"`
	// Matching is output only, filled by the server: repositories on record carrying every term.
	Matching int `json:"matching"`
	// Defaults names the terms whose value is the one a repository carries for choosing nothing,
	// as last read: such a scope takes in every repository nobody set, and every new one.
	Defaults []string `json:"defaults,omitempty"`
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
	// Properties are the account's property scopes, each covering what carries all its terms.
	// They add to Includes on a restricted account; a whole account needs none.
	Properties []PropertyClause `json:"properties"`
	DeclaredAt string           `json:"declaredAt"`
	// DeclaredBy is output only: the store sets it from the write path and ignores whatever a
	// caller sends. Empty means the row predates the column and nobody knows who wrote it.
	DeclaredBy string `json:"declaredBy"`
}

// Covers reports whether a repository falls inside this account's declaration.
//
// The Go form of `declaredFullName`, for the callers that cannot use SQL: a declaration being
// previewed has not been stored, and writing it in order to measure it would make canceling
// impossible. `TestCoveredByAgreesWithTheStoredScopePredicate` is what stops the two drifting.
//
// values are the repository's custom property values as GitHub last answered; nil when they were
// never read, which no property scope can match.
func (a DeclaredAccount) Covers(owner, repo string, values []PropertyValue) bool {
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
	for _, c := range a.Properties {
		if !c.Held && c.CarriedBy(values) {
			return true
		}
	}
	return false
}

// CarriedBy reports whether values hold every term: the name as GitHub matches it, without regard
// to case, and the value exactly, one row per value so a multi-select matches any of its own.
func (c PropertyClause) CarriedBy(values []PropertyValue) bool {
	if len(c.Terms) == 0 {
		return false
	}
	for _, t := range c.Terms {
		found := false
		for _, v := range values {
			if strings.EqualFold(v.Name, t.Name) && v.Value == t.Value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// HasProperties reports whether the account holds any property scope, which is what makes every
// repository in it worth reading whether or not it is covered yet.
func (a DeclaredAccount) HasProperties() bool {
	return a.Enabled && a.IncludesOnly && len(a.Properties) > 0
}

// CoveredBy reports whether any account in a declaration covers a repository.
func CoveredBy(accounts []DeclaredAccount, owner, repo string, values []PropertyValue) bool {
	for _, a := range accounts {
		if a.Covers(owner, repo, values) {
			return true
		}
	}
	return false
}

// coveredByProperty is the property half of the scope test: some honored scope on the account
// whose every term the repository carries. Spliced into each spelling below, never written twice;
// its aliases are its own so they cannot shadow a caller's, and fullName must be qualified.
func coveredByProperty(account, fullName string) string {
	return `EXISTS (SELECT 1 FROM abom_owned_property_clause_honored scope_clause
		 WHERE scope_clause.owner = ` + account + ` COLLATE NOCASE
		   AND NOT EXISTS (SELECT 1 FROM abom_owned_property_term scope_term
		                    WHERE scope_term.clause_id = scope_clause.id
		                      AND NOT EXISTS (SELECT 1 FROM abom_repository_property scope_value
		                                       WHERE scope_value.repository_full_name = ` + fullName + ` COLLATE NOCASE
		                                         AND scope_value.property_name = scope_term.property_name
		                                         AND scope_value.value = scope_term.value)))`
}

// ownedFrontierRow is the declared-scope predicate against a queue row, for use inside a larger
// statement. Written once so the tier, the queue and every "yours" split cannot drift apart; both
// sides are lower-cased because GitHub treats account names case-insensitively.
var ownedFrontierRow = `EXISTS (
	SELECT 1 FROM abom_owned_scope_honored o
	 WHERE o.enabled = 1
	   AND LOWER(o.owner) = LOWER(abom_frontier.owner)
	   AND (o.includes_only = 0
	        OR EXISTS (SELECT 1 FROM abom_owned_include i
	                    WHERE LOWER(i.owner) = LOWER(abom_frontier.owner)
	                      AND LOWER(i.repo) = LOWER(abom_frontier.repo))
	        OR ` + coveredByProperty("o.owner", "abom_frontier.owner || '/' || abom_frontier.repo") + `))
	AND NOT EXISTS (SELECT 1 FROM abom_repository_absence absent
	                 WHERE absent.repository = (abom_frontier.owner || '/' || abom_frontier.repo) COLLATE NOCASE)`

// ownedFullName renders the predicate against a column holding `owner/name`. Built rather than
// written out three times, so the tier, the queue view and the node users cannot drift into
// separate answers to the same question. A repository a full listing of its account no longer
// names is not the operator's, whatever the declaration says about the account.
func ownedFullName(col string) string {
	return `(` + declaredFullName(col) + ` AND NOT EXISTS (
		SELECT 1 FROM abom_repository_absence absent WHERE absent.repository = ` + col + ` COLLATE NOCASE))`
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
		                      AND LOWER(i.repo) = LOWER(` + repo + `))
		        OR ` + coveredByProperty("o.owner", col) + `))`
}

// DeclarationRefused is a declaration the store will not write, with the reason as a reader reads
// it. Anything else SetOwnedScope returns is the store failing, not the declaration.
type DeclarationRefused struct{ Reason string }

func (e *DeclarationRefused) Error() string { return "store: " + e.Reason }

func refuse(format string, args ...any) error {
	return &DeclarationRefused{Reason: fmt.Sprintf(format, args...)}
}

// SetOwnedScope records the declaration wholesale, discarding whatever preceded it.
// `by` is the code path doing the writing, not anything the caller supplied.
func (s *Store) SetOwnedScope(accounts []DeclaredAccount, by string) error {
	if err := validateOwnedScope(accounts, by); err != nil {
		return err
	}
	if err := s.refusePropertyScopes(accounts); err != nil {
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
		return refuse("%q is not a declaration source; expected %q, %q or %q",
			by, DeclaredByCLI, DeclaredByInterface, DeclaredByConfig)
	}
	for _, a := range accounts {
		if strings.TrimSpace(a.Owner) == "" {
			return refuse("a declared account needs an owner")
		}
		if a.Kind != AccountOrganization && a.Kind != AccountUser {
			return refuse("%q is not an account kind; expected %q or %q",
				a.Kind, AccountOrganization, AccountUser)
		}
		if err := validatePropertyScopes(a); err != nil {
			return err
		}
	}
	return nil
}

// validatePropertyScopes refuses a property scope that could never mean anything, whatever the
// schema later says.
func validatePropertyScopes(a DeclaredAccount) error {
	if len(a.Properties) == 0 {
		return nil
	}
	if a.Kind != AccountOrganization {
		return refuse("%s is a user account, and only an organization has custom properties", a.Owner)
	}
	if !a.IncludesOnly {
		return refuse("%s is declared whole, which already covers every repository a property scope could add", a.Owner)
	}
	for _, c := range a.Properties {
		if len(c.Terms) == 0 {
			return refuse("a property scope on %s names no property, so it would cover nothing", a.Owner)
		}
		for _, t := range c.Terms {
			if strings.TrimSpace(t.Name) == "" || t.Value == "" {
				return refuse("a property scope on %s needs a name and a value in every term", a.Owner)
			}
		}
	}
	return nil
}

// refusePropertyScopes refuses what the recorded schema already rules out. Anything it cannot
// decide is stored and held until a schema read does.
func (s *Store) refusePropertyScopes(accounts []DeclaredAccount) error {
	stored, err := s.ownedClauses()
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if len(a.Properties) == 0 {
			continue
		}
		schema, read, err := s.schemaFor(a.Owner)
		if err != nil {
			return err
		}
		for _, c := range a.Properties {
			// A scope held since its property changed is kept until the reader removes it.
			if slices.ContainsFunc(stored[strings.ToLower(a.Owner)], func(old PropertyClause) bool {
				return sameTerms(old.Terms, c.Terms)
			}) {
				continue
			}
			if reason, definitive := holdReason(a.Owner, c.Terms, schema, read); definitive {
				return refuse("%s", reason)
			}
		}
	}
	return nil
}

// sameTerms reports whether two scopes require the same values, in any order.
func sameTerms(a, b []PropertyTerm) bool {
	key := func(terms []PropertyTerm) []string {
		out := make([]string, 0, len(terms))
		for _, t := range terms {
			out = append(out, strings.ToLower(t.Name)+"\x00"+t.Value)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(key(a), key(b))
}

// schemaFor is an account's recorded schema keyed by lower-cased name, and whether it was ever read.
func (s *Store) schemaFor(owner string) (map[string]PropertyDefinition, bool, error) {
	defs, err := s.PropertySchema(owner)
	if err != nil {
		return nil, false, err
	}
	_, read, err := s.PropertySchemaReadAt(owner)
	if err != nil {
		return nil, false, err
	}
	out := make(map[string]PropertyDefinition, len(defs))
	for _, d := range defs {
		out[strings.ToLower(d.Name)] = d
	}
	return out, read, nil
}

// PropertyMayScope reports whether a property may decide what is yours, from who may set its
// values, and if not, why. One definition for the store's holds and the picker's offer.
func PropertyMayScope(name, valuesEditableBy string) (bool, string) {
	switch valuesEditableBy {
	case PropertyEditableByOrganization:
		return true, ""
	case "":
		return false, fmt.Sprintf("GitHub has not said who may set %s, so it cannot decide what is yours", name)
	default:
		return false, fmt.Sprintf("%s can be set by repository administrators, so it cannot decide what is yours", name)
	}
}

// PropertyScopeMatches counts an account's repositories on record carrying every term.
func (s *Store) PropertyScopeMatches(owner string, terms []PropertyTerm) (int, error) {
	if len(terms) == 0 {
		return 0, nil
	}
	query := `SELECT COUNT(*) FROM repository r
		WHERE LOWER(SUBSTR(r.repository_full_name, 1, INSTR(r.repository_full_name, '/') - 1)) = LOWER(?)
		  AND NOT EXISTS (SELECT 1 FROM abom_repository_absence a
		                  WHERE a.repository = r.repository_full_name COLLATE NOCASE)`
	args := []any{owner}
	for _, t := range terms {
		query += ` AND EXISTS (SELECT 1 FROM abom_repository_property v
			WHERE v.repository_full_name = r.repository_full_name COLLATE NOCASE AND v.property_name = ? AND v.value = ?)`
		args = append(args, t.Name, t.Value)
	}
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting what a property scope on %s covers: %w", owner, err)
	}
	return n, nil
}

// holdReason says why a scope counts nothing, in the words Settings shows, or "" when it counts.
// definitive marks what the schema settles outright, which a write refuses rather than holds.
func holdReason(owner string, terms []PropertyTerm, schema map[string]PropertyDefinition, read bool) (reason string, definitive bool) {
	if len(terms) == 0 {
		return "it names no property, so it covers nothing", true
	}
	for _, t := range terms {
		def, ok := schema[strings.ToLower(t.Name)]
		switch {
		// Rows without a read time predate it, and were written without asking who may set them.
		case !read:
			return fmt.Sprintf("%s's custom properties have not been read yet, so %s cannot be judged", owner, t.Name), false
		case !ok:
			return fmt.Sprintf("%s defines no custom property named %s", owner, t.Name), false
		}
		if may, why := PropertyMayScope(t.Name, def.ValuesEditableBy); !may {
			return why, def.ValuesEditableBy != ""
		}
		if def.ValueType != "multi_select" {
			for _, u := range terms {
				if strings.EqualFold(u.Name, t.Name) && u.Value != t.Value {
					return fmt.Sprintf("%s holds one value, so no repository carries both %s and %s", t.Name, t.Value, u.Value), true
				}
			}
		}
	}
	return "", false
}

func clearOwnedScope(tx *transaction) error {
	for _, stmt := range []string{`DELETE FROM abom_owned_property_term`,
		`DELETE FROM abom_owned_property_clause`, `DELETE FROM abom_owned_include`,
		`DELETE FROM abom_owned_scope`} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("clearing the declared scope: %w", err)
		}
	}
	return nil
}

func insertOwnedScope(tx *transaction, accounts []DeclaredAccount, by, at string) error {
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

func insertOwnedAccount(tx *transaction, owner string, a DeclaredAccount, by, at string) error {
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
	for _, c := range a.Properties {
		res, err := tx.Exec(`INSERT INTO abom_owned_property_clause (owner, declared_by, declared_at)
			VALUES (?, ?, ?)`, owner, by, at)
		if err != nil {
			return fmt.Errorf("declaring a property scope on %s: %w", owner, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("declaring a property scope on %s: %w", owner, err)
		}
		for _, t := range c.Terms {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO abom_owned_property_term
				(clause_id, property_name, value) VALUES (?, ?, ?)`,
				id, strings.TrimSpace(t.Name), t.Value); err != nil {
				return fmt.Errorf("declaring a property scope on %s: %w", owner, err)
			}
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
	clauses, err := s.ownedClauses()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		key := strings.ToLower(accounts[i].Owner)
		if got, ok := includes[key]; ok {
			accounts[i].Includes = got
		}
		accounts[i].Properties = []PropertyClause{}
		if got, ok := clauses[key]; ok {
			accounts[i].Properties = got
		}
	}
	if err := s.JudgePropertyScopes(accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// ownedClauses reads every property scope with its terms and whether SQL honors it, by account.
func (s *Store) ownedClauses() (map[string][]PropertyClause, error) {
	type row struct {
		id      int64
		owner   string
		clause  PropertyClause
		honored bool
	}
	rows, err := queryAll(s, "reading property scopes", func(r *sql.Rows) (row, error) {
		var x row
		var by sql.NullString
		err := r.Scan(&x.id, &x.owner, &by, &x.clause.DeclaredAt, &x.honored)
		x.clause.DeclaredBy = by.String
		x.clause.Terms = []PropertyTerm{}
		return x, err
	}, `SELECT c.id, c.owner, c.declared_by, c.declared_at,
	           EXISTS (SELECT 1 FROM abom_owned_property_clause_honored h WHERE h.id = c.id)
	      FROM abom_owned_property_clause c ORDER BY c.owner, c.id`)
	if err != nil {
		return nil, err
	}
	terms, err := queryAll(s, "reading property scope terms", func(r *sql.Rows) (struct {
		id int64
		t  PropertyTerm
	}, error) {
		var x struct {
			id int64
			t  PropertyTerm
		}
		return x, r.Scan(&x.id, &x.t.Name, &x.t.Value)
	}, `SELECT clause_id, property_name, value FROM abom_owned_property_term
	     ORDER BY clause_id, property_name, value`)
	if err != nil {
		return nil, err
	}
	byID := map[int64][]PropertyTerm{}
	for _, t := range terms {
		byID[t.id] = append(byID[t.id], t.t)
	}
	out := map[string][]PropertyClause{}
	for _, x := range rows {
		x.clause.Terms = append(x.clause.Terms, byID[x.id]...)
		// The view decides; the reason only explains it, and is filled in by JudgePropertyScopes.
		x.clause.Held = !x.honored
		key := strings.ToLower(x.owner)
		out[key] = append(out[key], x.clause)
	}
	return out, nil
}

// JudgePropertyScopes says, for every scope, whether it counts and why not, against the recorded
// schema. For stored scopes it only explains what the view already decided; for a declaration not
// yet stored it is the decision, which `TestAHeldScopeIsHeldInSQLExactlyWhenItHasAReason` holds to
// the view's.
func (s *Store) JudgePropertyScopes(accounts []DeclaredAccount) error {
	for i := range accounts {
		if len(accounts[i].Properties) == 0 {
			continue
		}
		schema, read, err := s.schemaFor(accounts[i].Owner)
		if err != nil {
			return err
		}
		for j := range accounts[i].Properties {
			c := &accounts[i].Properties[j]
			reason, _ := holdReason(accounts[i].Owner, c.Terms, schema, read)
			c.Held, c.HeldReason = reason != "", reason
			c.Defaults = nil
			for _, t := range c.Terms {
				if def, ok := schema[strings.ToLower(t.Name)]; ok && def.IsDefault(t.Value) &&
					!slices.Contains(c.Defaults, def.Name) {
					c.Defaults = append(c.Defaults, def.Name)
				}
			}
		}
	}
	return nil
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
	}, `SELECT r.repository_full_name FROM repository r
	     WHERE `+ownedFullName("r.repository_full_name")+`
	     ORDER BY r.repository_full_name`)
}

// RepositoryIsOwned reports whether one `owner/name` falls inside the declared scope.
func (s *Store) RepositoryIsOwned(fullName string) (bool, error) {
	if _, _, ok := splitRepository(fullName); !ok {
		return false, nil
	}
	owned, err := s.OwnedAmong([]string{fullName})
	if err != nil {
		return false, fmt.Errorf("reading ownership of %q: %w", fullName, err)
	}
	return owned[strings.ToLower(fullName)], nil
}

// OwnedAmong reports which of these `owner/name` repositories the declared scope covers, keyed
// lower-cased. One statement for the whole set, so a round asks once rather than once per batch.
func (s *Store) OwnedAmong(fullNames []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(fullNames) == 0 {
		return out, nil
	}
	values := make([]string, 0, len(fullNames))
	args := make([]any, 0, len(fullNames))
	for _, name := range fullNames {
		values = append(values, "(?)")
		args = append(args, name)
	}
	names, err := queryAll(s, "reading ownership of repositories", func(r *sql.Rows) (string, error) {
		var name string
		return name, r.Scan(&name)
	}, `WITH asked(name) AS (VALUES `+strings.Join(values, ", ")+`)
		SELECT name FROM asked WHERE `+ownedFullName("asked.name"), args...)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		out[strings.ToLower(name)] = true
	}
	return out, nil
}

// splitRepository refuses anything that does not name both halves.
func splitRepository(fullName string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(fullName, "/")
	if !found || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}
