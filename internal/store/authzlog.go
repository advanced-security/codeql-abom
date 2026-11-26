package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// The authorization log: every signed assertion, in the order the decision point made them, each
// with the chain value covering it and everything before it. Append-only, and enforced by triggers
// rather than by convention, because the log is the evidence `audit verify` re-checks.
const authzLogDDL = `
CREATE TABLE IF NOT EXISTS abom_authz_log (
    seq            INTEGER PRIMARY KEY,
    predicate_type TEXT NOT NULL,
    digest         TEXT NOT NULL,
    chain          TEXT NOT NULL UNIQUE,
    envelope       TEXT NOT NULL,
    recorded_at    TEXT NOT NULL
);`

const authzLogIndexDDL = `CREATE INDEX IF NOT EXISTS idx_authz_log_digest ON abom_authz_log (digest)`

// authzDecisionDDL indexes what each logged decision says it decided, so SQL can honor only an allow
// of the operation it gates. Read from the envelope unverified: it narrows what SQL honors, and the
// Go side verifies the decision before anything acts on it. Append-only, like the log.
const authzDecisionDDL = `
CREATE TABLE IF NOT EXISTS abom_authz_decision (
    digest    TEXT PRIMARY KEY,
    operation TEXT NOT NULL,
    verdict   TEXT NOT NULL
);`

const authzLogTriggersSQLite = `
CREATE TRIGGER IF NOT EXISTS abom_authz_log_no_update BEFORE UPDATE ON abom_authz_log
BEGIN SELECT RAISE(ABORT, 'abom_authz_log is append-only'); END;
CREATE TRIGGER IF NOT EXISTS abom_authz_log_no_delete BEFORE DELETE ON abom_authz_log
BEGIN SELECT RAISE(ABORT, 'abom_authz_log is append-only'); END;
CREATE TRIGGER IF NOT EXISTS abom_authz_decision_no_update BEFORE UPDATE ON abom_authz_decision
BEGIN SELECT RAISE(ABORT, 'abom_authz_decision is append-only'); END;
CREATE TRIGGER IF NOT EXISTS abom_authz_decision_no_delete BEFORE DELETE ON abom_authz_decision
BEGIN SELECT RAISE(ABORT, 'abom_authz_decision is append-only'); END;`

const authzLogTriggersPostgres = `
CREATE OR REPLACE FUNCTION abom_refuse_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION '% is append-only', TG_TABLE_NAME; END $$;
CREATE OR REPLACE TRIGGER abom_authz_log_append_only BEFORE UPDATE OR DELETE ON abom_authz_log
FOR EACH ROW EXECUTE FUNCTION abom_refuse_change();
CREATE OR REPLACE TRIGGER abom_authz_log_no_truncate BEFORE TRUNCATE ON abom_authz_log
FOR EACH STATEMENT EXECUTE FUNCTION abom_refuse_change();
CREATE OR REPLACE TRIGGER abom_authz_decision_append_only BEFORE UPDATE OR DELETE ON abom_authz_decision
FOR EACH ROW EXECUTE FUNCTION abom_refuse_change();
CREATE OR REPLACE TRIGGER abom_authz_decision_no_truncate BEFORE TRUNCATE ON abom_authz_decision
FOR EACH STATEMENT EXECUTE FUNCTION abom_refuse_change();`

// authzEntryColumns are an authz.Entry's, in the order scanAuthzEntry reads them.
const authzEntryColumns = "seq, predicate_type, digest, chain, envelope, recorded_at"

// AppendAssertion records one assertion after the head, chained from it. Two writers do not extend
// the same head: SQLite takes the write lock when the transaction begins, and every PostgreSQL write
// transaction takes the publication lock first, so the second waits and reads the first's entry.
func (s *Store) AppendAssertion(predicateType, digest string, envelope []byte, at time.Time) (authz.Entry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return authz.Entry{}, fmt.Errorf("logging an assertion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	prev, seq := authz.Genesis, int64(0)
	err = tx.QueryRow(`SELECT seq, chain FROM abom_authz_log ORDER BY seq DESC LIMIT 1`).Scan(&seq, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return authz.Entry{}, fmt.Errorf("reading the authorization log's head: %w", err)
	}
	e := authz.Entry{Seq: seq + 1, PredicateType: predicateType, Digest: digest,
		Chain: authz.NextChain(prev, digest), Envelope: envelope, RecordedAt: at.UTC().Format(time.RFC3339Nano)}
	if _, err := tx.Exec(`INSERT INTO abom_authz_log (`+authzEntryColumns+`) VALUES (?, ?, ?, ?, ?, ?)`, e.Seq, e.PredicateType, e.Digest, e.Chain, string(e.Envelope), e.RecordedAt); err != nil {
		return authz.Entry{}, fmt.Errorf("logging an assertion: %w", err)
	}
	if err := indexDecision(tx, e); err != nil {
		return authz.Entry{}, err
	}
	return e, tx.Commit()
}

// indexDecision records what a logged decision says it decided. Other assertions are not indexed,
// and neither is a decision that cannot be read, which SQL then honors nowhere.
func indexDecision(tx *transaction, e authz.Entry) error {
	if e.PredicateType != authz.PredicateDecision {
		return nil
	}
	claim, unreadable := authz.ClaimOf(e.Envelope)
	if unreadable == nil {
		if _, err := tx.Exec(`INSERT INTO abom_authz_decision (digest, operation, verdict) VALUES (?, ?, ?)
			ON CONFLICT (digest) DO NOTHING`, e.Digest, claim.Operation, claim.Verdict); err != nil {
			return fmt.Errorf("indexing decision %d: %w", e.Seq, err)
		}
	}
	return nil
}

// indexLoggedDecisions indexes decisions logged before the index existed, so a database from before
// it keeps honoring what it decided.
func (s *Store) indexLoggedDecisions() error {
	entries, err := queryAll(s, "reading decisions not yet indexed",
		func(r *sql.Rows) (authz.Entry, error) { return scanAuthzEntry(r) },
		`SELECT `+authzEntryColumns+` FROM abom_authz_log l
		  WHERE l.predicate_type = ? AND NOT EXISTS (SELECT 1 FROM abom_authz_decision d WHERE d.digest = l.digest)
		  ORDER BY l.seq`, authz.PredicateDecision)
	if err != nil || len(entries) == 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range entries {
		if err := indexDecision(tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Assertions reads the log after a sequence number, oldest first; a limit of zero reads all of it.
func (s *Store) Assertions(after int64, limit int) ([]authz.Entry, error) {
	query := `SELECT ` + authzEntryColumns + ` FROM abom_authz_log WHERE seq > ? ORDER BY seq`
	args := []any{after}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return queryAll(s, "reading the authorization log", func(r *sql.Rows) (authz.Entry, error) {
		return scanAuthzEntry(r)
	}, query, args...)
}

// Head is the newest entry of the log, and false when it is empty.
func (s *Store) Head() (authz.Entry, bool, error) {
	e, err := scanAuthzEntry(s.db.QueryRow(`SELECT ` + authzEntryColumns + ` FROM abom_authz_log ORDER BY seq DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return authz.Entry{}, false, nil
	}
	if err != nil {
		return authz.Entry{}, false, fmt.Errorf("reading the authorization log's head: %w", err)
	}
	return e, true, nil
}

func scanAuthzEntry(row interface{ Scan(...any) error }) (authz.Entry, error) {
	var e authz.Entry
	var envelope string
	err := row.Scan(&e.Seq, &e.PredicateType, &e.Digest, &e.Chain, &envelope, &e.RecordedAt)
	e.Envelope = []byte(envelope)
	return e, err
}
