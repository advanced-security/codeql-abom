package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// policyDDL holds a rule, what it binds to, and whether anybody has turned it on.
//
// A policy is a saved view plus a scope plus a decision. The predicate is stored rather than
// referenced by name so that adopting one freezes what it asked: a policy that followed a saved
// view would change meaning when somebody edited the view, and nobody would be told.
//
// `adopted_simulation` is the point of the table. A policy that cannot say what it was expected to
// block cannot be argued about afterwards, and a gate enabled on a backlog is switched off within a
// week. See plans/api/policy-lifecycle.md.
const policyDDL = `
CREATE TABLE IF NOT EXISTS abom_policy (
  name        TEXT PRIMARY KEY,
  predicate   TEXT NOT NULL,
  scope_kind  TEXT NOT NULL,
  scope_value TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL,
  created_by  TEXT,
  created_at  TEXT NOT NULL,
  adopted_by  TEXT,
  adopted_at  TEXT,
  adopted_simulation INTEGER
);

CREATE TABLE IF NOT EXISTS abom_policy_simulation (
  row_id       INTEGER PRIMARY KEY,
  policy       TEXT NOT NULL,
  ran_at       TEXT NOT NULL,
  nodes        INTEGER NOT NULL,
  findings     INTEGER NOT NULL,
  repositories INTEGER NOT NULL,
  -- Refused is repositories the policy would stop, which is repositories minus the ones a waiver
  -- covers. Both are kept because a rule that matches 40 and refuses 2 is a different rule from
  -- one that matches 2, and only the pair says which was adopted.
  refused      INTEGER NOT NULL,
  waived       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_policy_simulation_policy
  ON abom_policy_simulation (policy, ran_at DESC);

CREATE TABLE IF NOT EXISTS abom_policy_waiver (
  row_id     INTEGER PRIMARY KEY,
  policy     TEXT NOT NULL,
  kind       TEXT NOT NULL,
  -- One repository, named in full. Never a pattern: a pattern cannot be audited and it silently
  -- covers a repository created after it was written, which is a waiver nobody granted.
  repository TEXT NOT NULL COLLATE NOCASE,
  reason     TEXT NOT NULL,
  granted_by TEXT,
  granted_at TEXT NOT NULL,
  -- NULL for an exclusion, required for an exception. Read at evaluation time rather than swept,
  -- so an exception stops applying the moment it runs out and not when somebody runs a job.
  expires_at TEXT,
  UNIQUE (policy, kind, repository)
);`

// Policy states. A draft is a question; adopted is a decision.
const (
	PolicyDraft   = "draft"
	PolicyAdopted = "adopted"
)

// What a policy binds to.
const (
	PolicyScopeOrganization = "organization"
	PolicyScopeUnit         = "unit"
	PolicyScopeRepository   = "repository"
)

// Waiver kinds. The difference is the whole design: one says the rule does not apply, the other
// says it applies and is waived until a stated time.
const (
	WaiverExclusion = "exclusion"
	WaiverException = "exception"
)

// Policy is a rule, its scope, and whether it is in force.
type Policy struct {
	Name      string `json:"name"`
	Predicate string `json:"predicate"`
	ScopeKind string `json:"scopeKind"`
	// ScopeValue is the unit or repository the scope names. Empty for an organization.
	ScopeValue string    `json:"scopeValue,omitempty"`
	State      string    `json:"state"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitzero"`
	AdoptedBy  string    `json:"adoptedBy,omitempty"`
	AdoptedAt  time.Time `json:"adoptedAt,omitzero"`
	// AdoptedSimulation is the run that was on the table when somebody said yes.
	AdoptedSimulation int64 `json:"adoptedSimulation,omitempty"`
}

// Simulation is what a policy matched on one occasion, with nothing at stake.
type Simulation struct {
	ID           int64     `json:"id"`
	Policy       string    `json:"policy"`
	RanAt        time.Time `json:"ranAt,omitzero"`
	Nodes        int       `json:"nodes"`
	Findings     int       `json:"findings"`
	Repositories int       `json:"repositories"`
	Refused      int       `json:"refused"`
	Waived       int       `json:"waived"`
}

// Waiver is one enumerated exclusion or exception.
type Waiver struct {
	ID         int64     `json:"id"`
	Policy     string    `json:"policy"`
	Kind       string    `json:"kind"`
	Repository string    `json:"repository"`
	Reason     string    `json:"reason"`
	GrantedBy  string    `json:"grantedBy,omitempty"`
	GrantedAt  time.Time `json:"grantedAt,omitzero"`
	ExpiresAt  time.Time `json:"expiresAt,omitzero"`
	// InForce is computed at read time from the expiry, never stored. A waiver whose state was
	// written down stays in force until something runs, which is the failure expiry exists to
	// prevent.
	InForce bool `json:"inForce"`
}

// SavePolicy records a draft, replacing one of the same name.
//
// A policy always arrives as a draft. Accepting a state from the caller would let a client adopt one
// by writing a field, which is the simulation gate bypassed by spelling.
func (s *Store) SavePolicy(p Policy) error {
	switch {
	case strings.TrimSpace(p.Name) == "":
		return errors.New("store: a policy needs a name")
	case strings.TrimSpace(p.Predicate) == "":
		return errors.New("store: a policy needs a predicate, or it refuses nothing")
	}
	switch p.ScopeKind {
	case PolicyScopeOrganization:
	case PolicyScopeUnit, PolicyScopeRepository:
		if strings.TrimSpace(p.ScopeValue) == "" {
			return fmt.Errorf("store: a %s scope has to name one", p.ScopeKind)
		}
	default:
		return fmt.Errorf("store: %q is not a policy scope; expected %q, %q or %q",
			p.ScopeKind, PolicyScopeOrganization, PolicyScopeUnit, PolicyScopeRepository)
	}

	_, err := s.db.Exec(`
INSERT INTO abom_policy (name, predicate, scope_kind, scope_value, state, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  predicate = excluded.predicate,
  scope_kind = excluded.scope_kind,
  scope_value = excluded.scope_value,
  -- Editing the rule un-adopts it. What was simulated is no longer what is written, and leaving
  -- it in force would enforce something nobody agreed to.
  state = ?, adopted_by = NULL, adopted_at = NULL, adopted_simulation = NULL`,
		p.Name, p.Predicate, p.ScopeKind, p.ScopeValue, PolicyDraft, p.CreatedBy,
		time.Now().UTC().Format(time.RFC3339Nano), PolicyDraft)
	return err
}

// PolicyNamed reports one policy, and whether it exists.
func (s *Store) PolicyNamed(name string) (Policy, bool, error) {
	var (
		p                    = Policy{Name: name}
		createdBy, adoptedBy sql.NullString
		createdAt, adoptedAt sql.NullString
		scopeValue           sql.NullString
		adoptedSimulation    sql.NullInt64
	)
	err := s.db.QueryRow(`
SELECT predicate, scope_kind, scope_value, state, created_by, created_at,
       adopted_by, adopted_at, adopted_simulation
  FROM abom_policy WHERE name = ?`, name).
		Scan(&p.Predicate, &p.ScopeKind, &scopeValue, &p.State, &createdBy, &createdAt,
			&adoptedBy, &adoptedAt, &adoptedSimulation)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Policy{}, false, nil
	case err != nil:
		return Policy{}, false, err
	}
	p.ScopeValue = scopeValue.String
	p.CreatedBy, p.AdoptedBy = createdBy.String, adoptedBy.String
	p.CreatedAt, p.AdoptedAt = parseMark(createdAt), parseMark(adoptedAt)
	p.AdoptedSimulation = adoptedSimulation.Int64
	return p, true, nil
}

// Policies lists every policy, drafts included.
func (s *Store) Policies() ([]Policy, error) {
	rows, err := s.db.Query(`SELECT name FROM abom_policy ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Policy, 0, len(names))
	for _, name := range names {
		p, ok, err := s.PolicyNamed(name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// RecordSimulation stores what a policy matched on one occasion.
func (s *Store) RecordSimulation(run Simulation) (int64, error) {
	if _, ok, err := s.PolicyNamed(run.Policy); err != nil {
		return 0, err
	} else if !ok {
		return 0, fmt.Errorf("store: no policy named %q to simulate", run.Policy)
	}
	res, err := s.db.Exec(`
INSERT INTO abom_policy_simulation (policy, ran_at, nodes, findings, repositories, refused, waived)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.Policy, time.Now().UTC().Format(time.RFC3339Nano),
		run.Nodes, run.Findings, run.Repositories, run.Refused, run.Waived)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LatestSimulation reports the most recent run for a policy, and whether there has been one.
func (s *Store) LatestSimulation(policy string) (Simulation, bool, error) {
	var (
		run   = Simulation{Policy: policy}
		ranAt sql.NullString
	)
	err := s.db.QueryRow(`
SELECT row_id, ran_at, nodes, findings, repositories, refused, waived
  FROM abom_policy_simulation WHERE policy = ? ORDER BY ran_at DESC, row_id DESC LIMIT 1`, policy).
		Scan(&run.ID, &ranAt, &run.Nodes, &run.Findings, &run.Repositories, &run.Refused, &run.Waived)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Simulation{}, false, nil
	case err != nil:
		return Simulation{}, false, err
	}
	run.RanAt = parseMark(ranAt)
	return run, true, nil
}

// AdoptPolicy turns a draft on, against the simulation that was on the table.
func (s *Store) AdoptPolicy(name, by string) error {
	policy, ok, err := s.PolicyNamed(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("store: no policy named %q", name)
	}
	run, simulated, err := s.LatestSimulation(name)
	if err != nil {
		return err
	}
	if !simulated {
		return fmt.Errorf(
			"store: %q has never been simulated, so nobody knows what adopting it would refuse",
			policy.Name)
	}

	_, err = s.db.Exec(`
UPDATE abom_policy SET state = ?, adopted_by = ?, adopted_at = ?, adopted_simulation = ?
 WHERE name = ?`,
		PolicyAdopted, by, time.Now().UTC().Format(time.RFC3339Nano), run.ID, name)
	return err
}

// GrantWaiver records an exclusion or an exception against one named repository.
func (s *Store) GrantWaiver(w Waiver) error {
	return s.grantWaiverAt(w, time.Now().UTC())
}

// grantWaiverAt is GrantWaiver with the grant time supplied, so a test can place one in the past
// without waiting for an expiry to pass.
func (s *Store) grantWaiverAt(w Waiver, at time.Time) error {
	if err := checkWaiver(w); err != nil {
		return err
	}
	var expires any
	if !w.ExpiresAt.IsZero() {
		expires = w.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
INSERT INTO abom_policy_waiver (policy, kind, repository, reason, granted_by, granted_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(policy, kind, repository) DO UPDATE SET
  reason = excluded.reason, granted_by = excluded.granted_by,
  granted_at = excluded.granted_at, expires_at = excluded.expires_at`,
		w.Policy, w.Kind, w.Repository, w.Reason, w.GrantedBy,
		at.UTC().Format(time.RFC3339Nano), expires)
	return err
}

// checkWaiver holds the two rules the whole design rests on: enumerated, and expiring when it is an
// exception.
func checkWaiver(w Waiver) error {
	switch w.Kind {
	case WaiverExclusion:
		if !w.ExpiresAt.IsZero() {
			return errors.New(
				"store: an exclusion does not expire; grant an exception if it should")
		}
	case WaiverException:
		if w.ExpiresAt.IsZero() {
			return errors.New(
				"store: an exception has to expire, or it is an exclusion nobody admitted to")
		}
	default:
		return fmt.Errorf("store: %q is not a waiver kind; expected %q or %q",
			w.Kind, WaiverExclusion, WaiverException)
	}
	if strings.TrimSpace(w.Reason) == "" {
		return errors.New("store: a waiver needs a reason, or nobody can review it")
	}
	return checkEnumerated(w.Repository)
}

// patternCharacters are what separates naming a repository from describing a set of them.
const patternCharacters = "*?[]"

// checkEnumerated refuses anything that is not one repository named in full.
//
// The counter-argument is that enumeration does not scale; the answer is that scope is the thing
// that is supposed to scale, and a waiver is supposed to be uncomfortable.
func checkEnumerated(repository string) error {
	name := strings.TrimSpace(repository)
	if name == "" {
		return errors.New("store: a waiver has to name one repository")
	}
	if strings.ContainsAny(name, patternCharacters) {
		return fmt.Errorf(
			"store: %q is a pattern, and a pattern covers repositories nobody named; "+
				"list each repository instead", repository)
	}
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return fmt.Errorf("store: %q is not one repository as owner/name", repository)
	}
	return nil
}

// Waivers lists every waiver for one policy, granted or expired.
func (s *Store) Waivers(policy string) ([]Waiver, error) {
	return s.waiversWhere(`WHERE policy = ?`, policy)
}

// AllWaivers is the audit answer: every exclusion and exception, across every policy.
//
// Expired ones are listed, not dropped. Removing a waiver when its time runs out erases the fact
// that somebody once granted it, which is exactly what an audit is for.
func (s *Store) AllWaivers() ([]Waiver, error) {
	return s.waiversWhere(``)
}

func (s *Store) waiversWhere(where string, args ...any) ([]Waiver, error) {
	rows, err := s.db.Query(`
SELECT row_id, policy, kind, repository, reason, granted_by, granted_at, expires_at
  FROM abom_policy_waiver `+where+`
 ORDER BY policy, kind, repository`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	out := []Waiver{}
	for rows.Next() {
		var (
			w                    Waiver
			grantedBy, grantedAt sql.NullString
			expiresAt            sql.NullString
		)
		if err := rows.Scan(&w.ID, &w.Policy, &w.Kind, &w.Repository, &w.Reason,
			&grantedBy, &grantedAt, &expiresAt); err != nil {
			return nil, err
		}
		w.GrantedBy = grantedBy.String
		w.GrantedAt = parseMark(grantedAt)
		w.ExpiresAt = parseMark(expiresAt)
		w.InForce = w.ExpiresAt.IsZero() || w.ExpiresAt.After(now)
		out = append(out, w)
	}
	return out, rows.Err()
}

// WaivedRepositories names the repositories a policy is currently not applied to.
//
// Computed from the expiry at read time, so an exception stops applying the moment it runs out
// rather than when somebody remembers to sweep.
func (s *Store) WaivedRepositories(policy string) (map[string]Waiver, error) {
	all, err := s.Waivers(policy)
	if err != nil {
		return nil, err
	}
	out := map[string]Waiver{}
	for _, w := range all {
		if w.InForce {
			out[strings.ToLower(w.Repository)] = w
		}
	}
	return out, nil
}
