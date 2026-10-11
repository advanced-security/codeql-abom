package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

type ReplicaState struct {
	Identity   string
	Database   string
	Schema     string
	Recovery   bool
	Position   string
	ReplayAt   *time.Time
	ObservedAt time.Time
}

func (s *Store) ensureReplicaIdentity() error {
	if _, err := s.db.DB.Exec(`CREATE TABLE IF NOT EXISTS abom_store_identity (
		singleton INTEGER PRIMARY KEY, identity TEXT NOT NULL)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO abom_store_identity (singleton, identity) VALUES (1, ?)
		ON CONFLICT(singleton) DO NOTHING`, rand.Text())
	return err
}

func (s *Store) ReplicationState() (ReplicaState, error) {
	if !s.db.postgres {
		return ReplicaState{}, errors.New("read replicas require a PostgreSQL backend")
	}
	var state ReplicaState
	err := s.db.DB.QueryRow(`SELECT (SELECT identity FROM abom_store_identity WHERE singleton = 1),
		current_database(), current_schema(), pg_is_in_recovery(),
		CASE WHEN pg_is_in_recovery() THEN COALESCE(pg_last_wal_replay_lsn()::text, '')
		     ELSE pg_current_wal_lsn()::text END,
		pg_last_xact_replay_timestamp(), clock_timestamp()`).
		Scan(&state.Identity, &state.Database, &state.Schema,
			&state.Recovery, &state.Position, &state.ReplayAt, &state.ObservedAt)
	if err != nil {
		return state, fmt.Errorf("reading PostgreSQL replication state: %w", err)
	}
	return state, nil
}
