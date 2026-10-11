package store

import (
	"database/sql"
	"fmt"
)

func (s *Store) installChangeFeed() error {
	if err := s.ensureReplicaIdentity(); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS abom_commit_sequence (
			singleton INTEGER PRIMARY KEY, value BIGINT NOT NULL, reset_at BIGINT NOT NULL DEFAULT 0)`,
		`INSERT INTO abom_commit_sequence (singleton, value) VALUES (1, 0)
			ON CONFLICT(singleton) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS abom_graph_change (
			sequence BIGINT NOT NULL, object_kind TEXT NOT NULL, object_id BIGINT NOT NULL,
			created_sequence BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY(object_kind, object_id))`,
		`CREATE INDEX IF NOT EXISTS idx_graph_change_sequence
			ON abom_graph_change(sequence, object_kind, object_id)`,
	} {
		if _, err := s.db.DB.Exec(statement); err != nil {
			return fmt.Errorf("creating the commit-ordered change feed: %w", err)
		}
	}
	if s.db.postgres {
		if _, err := s.db.DB.Exec(`ALTER TABLE abom_graph_change
			ADD COLUMN IF NOT EXISTS created_sequence BIGINT NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := s.db.DB.Exec(`ALTER TABLE abom_commit_sequence
			ADD COLUMN IF NOT EXISTS reset_at BIGINT NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := s.db.DB.Exec(`ALTER TABLE abom_graph_generation
			ADD COLUMN IF NOT EXISTS commit_watermark BIGINT`); err != nil {
			return err
		}
		if err := s.adoptCommitCursor(); err != nil {
			return err
		}
		return s.postgresChangeTriggers()
	}
	for _, column := range []struct{ table, name, declaration string }{
		{"abom_graph_generation", "commit_watermark", "INTEGER"},
		{"abom_commit_sequence", "reset_at", "INTEGER NOT NULL DEFAULT 0"},
		{"abom_graph_change", "created_sequence", "INTEGER NOT NULL DEFAULT 0"},
	} {
		has, err := s.hasColumn(column.table, column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.declaration); err != nil {
				return err
			}
		}
	}
	if err := s.adoptCommitCursor(); err != nil {
		return err
	}
	tx, err := s.db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, source := range graphChangeSources {
		kind, table := source.kind, source.table
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW"
			if operation == "DELETE" {
				row = "OLD"
			}
			reset := ""
			if kind == "containment" && operation == "DELETE" {
				reset = `UPDATE abom_commit_sequence SET reset_at = value WHERE singleton = 1;`
			}
			birth, updateBirth := "0", "abom_graph_change.created_sequence"
			if table == "abom_node" && operation == "INSERT" {
				birth, updateBirth = "(SELECT value FROM abom_commit_sequence WHERE singleton = 1)", "excluded.created_sequence"
			}
			if _, err := tx.Exec("DROP TRIGGER IF EXISTS abom_change_" + table + "_" + operation); err != nil {
				return err
			}
			statement := fmt.Sprintf(`CREATE TRIGGER abom_change_%s_%s
				AFTER %s ON %s BEGIN
				UPDATE abom_commit_sequence SET value = value + 1 WHERE singleton = 1;
				%s
				INSERT INTO abom_graph_change (sequence, object_kind, object_id, created_sequence)
				VALUES ((SELECT value FROM abom_commit_sequence WHERE singleton = 1), '%s', %s.%s, %s)
				ON CONFLICT(object_kind, object_id) DO UPDATE SET sequence = excluded.sequence, created_sequence = %s;
				END`, table, operation, operation, table, reset, kind, row, source.key, birth, updateBirth)
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("tracking committed %s changes: %w", kind, err)
			}
		}
	}
	if err := s.sqliteScopeTriggers(tx); err != nil {
		return err
	}
	return tx.Commit()
}

var graphChangeSources = []struct{ table, kind, key string }{
	{"abom_node", "node", "row_id"},
	{"abom_edge", "edge", "row_id"},
	{"abom_containment", "containment", "row_id"},
	{"abom_edge_sighting", "edge", "edge_row_id"},
	{"abom_blast_radius", "node", "node_row_id"},
	{"abom_finding_node", "node", "node_row_id"},
}

var graphScopeTables = []string{
	"repository", "abom_owned_scope", "abom_owned_include", "abom_owned_property_clause",
	"abom_owned_property_term", "abom_repository_property", "abom_property_schema",
	"abom_account", "abom_commit_content", "abom_query_run",
}

func (s *Store) sqliteScopeTriggers(tx *sql.Tx) error {
	for _, table := range append(append([]string{}, graphScopeTables...), "abom_frontier") {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			resetCondition := "1"
			when := ""
			if table == "abom_frontier" && operation == "UPDATE" {
				when = `WHEN OLD.status IS NOT NEW.status OR OLD.resolved_sha IS NOT NEW.resolved_sha`
				resetCondition = "OLD.resolved_sha IS NOT NEW.resolved_sha"
			}
			if table == "abom_query_run" {
				if operation == "INSERT" {
					resetCondition = "0"
				} else if operation == "UPDATE" {
					resetCondition = `OLD.analysis_mode IS NOT NEW.analysis_mode OR OLD.held IS NOT NEW.held
						OR OLD.db_commit IS NOT NEW.db_commit`
				}
			}
			if table == "repository" && operation == "UPDATE" {
				resetCondition = `OLD.repository_full_name IS NOT NEW.repository_full_name OR OLD.origin IS NOT NEW.origin`
			}
			change := `UPDATE abom_commit_sequence SET value = value + 1 WHERE singleton = 1;`
			change += `UPDATE abom_commit_sequence SET reset_at = value
				WHERE singleton = 1 AND (` + resetCondition + `);`
			if _, err := tx.Exec("DROP TRIGGER IF EXISTS abom_scope_" + table + "_" + operation); err != nil {
				return err
			}
			statement := fmt.Sprintf(`CREATE TRIGGER abom_scope_%s_%s
				AFTER %s ON %s %s BEGIN %s END`, table, operation, operation, table, when, change)
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("tracking graph scope changes in %s: %w", table, err)
			}
		}
	}
	return nil
}

func (s *Store) adoptCommitCursor() error {
	_, err := s.db.Exec(`UPDATE abom_commit_sequence
		SET value = (SELECT MAX(generation) + 1 FROM abom_graph_generation),
		    reset_at = (SELECT MAX(generation) + 1 FROM abom_graph_generation)
		WHERE singleton = 1 AND value < (SELECT MAX(generation) FROM abom_graph_generation)`)
	return err
}

func (s *Store) postgresChangeTriggers() error {
	// The counter row stays locked until commit, so a later sequence cannot become visible first.
	_, err := s.db.DB.Exec(`CREATE OR REPLACE FUNCTION abom_record_graph_change() RETURNS trigger
		LANGUAGE plpgsql AS $$
		DECLARE revision bigint; identity bigint;
		BEGIN
			UPDATE abom_commit_sequence SET value = value + 1 WHERE singleton = 1 RETURNING value INTO revision;
			IF TG_OP = 'DELETE' AND TG_ARGV[0] = 'containment' THEN
				UPDATE abom_commit_sequence SET reset_at = revision WHERE singleton = 1;
			END IF;
			IF TG_OP = 'DELETE' THEN identity := (to_jsonb(OLD)->>TG_ARGV[1])::bigint;
			ELSE identity := (to_jsonb(NEW)->>TG_ARGV[1])::bigint; END IF;
			INSERT INTO abom_graph_change(sequence, object_kind, object_id, created_sequence)
				VALUES (revision, TG_ARGV[0], identity,
					CASE WHEN TG_TABLE_NAME = 'abom_node' AND TG_OP = 'INSERT' THEN revision ELSE 0 END)
				ON CONFLICT(object_kind, object_id) DO UPDATE SET sequence = excluded.sequence,
					created_sequence = CASE WHEN TG_TABLE_NAME = 'abom_node' AND TG_OP = 'INSERT'
						THEN excluded.created_sequence ELSE abom_graph_change.created_sequence END;
			IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
		END $$`)
	if err != nil {
		return fmt.Errorf("creating PostgreSQL commit tracking: %w", err)
	}
	for _, source := range graphChangeSources {
		kind, table := source.kind, source.table
		if source.key == "row_id" {
			if _, err := s.db.DB.Exec("DROP TRIGGER IF EXISTS abom_change_" + kind + " ON " + table); err != nil {
				return err
			}
		}
		statement := fmt.Sprintf(`CREATE OR REPLACE TRIGGER abom_change_%s
			AFTER INSERT OR UPDATE OR DELETE ON %s
			FOR EACH ROW EXECUTE FUNCTION abom_record_graph_change('%s', '%s')`, table, table, kind, source.key)
		if _, err := s.db.DB.Exec(statement); err != nil {
			return fmt.Errorf("tracking committed %s changes: %w", kind, err)
		}
	}
	if _, err := s.db.DB.Exec(`CREATE OR REPLACE FUNCTION abom_record_graph_scope() RETURNS trigger
		LANGUAGE plpgsql AS $$
		DECLARE reset_cursor boolean := true;
		BEGIN
			IF TG_TABLE_NAME = 'abom_frontier' AND TG_OP = 'UPDATE' THEN
				IF OLD.status IS NOT DISTINCT FROM NEW.status
					AND OLD.resolved_sha IS NOT DISTINCT FROM NEW.resolved_sha THEN RETURN NEW; END IF;
				reset_cursor := OLD.resolved_sha IS DISTINCT FROM NEW.resolved_sha;
			ELSIF TG_TABLE_NAME = 'abom_query_run' THEN
				IF TG_OP = 'INSERT' THEN reset_cursor := false;
				ELSIF TG_OP = 'UPDATE' THEN
					reset_cursor := OLD.analysis_mode IS DISTINCT FROM NEW.analysis_mode
						OR OLD.held IS DISTINCT FROM NEW.held OR OLD.db_commit IS DISTINCT FROM NEW.db_commit;
				END IF;
			ELSIF TG_TABLE_NAME = 'repository' AND TG_OP = 'UPDATE' THEN
				reset_cursor := OLD.repository_full_name IS DISTINCT FROM NEW.repository_full_name
					OR OLD.origin IS DISTINCT FROM NEW.origin;
			END IF;
			UPDATE abom_commit_sequence SET value = value + 1,
				reset_at = CASE WHEN reset_cursor THEN value + 1 ELSE reset_at END WHERE singleton = 1;
			IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
		END $$`); err != nil {
		return err
	}
	for _, table := range append(append([]string{}, graphScopeTables...), "abom_frontier") {
		statement := fmt.Sprintf(`CREATE OR REPLACE TRIGGER abom_scope_%s
			AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION abom_record_graph_scope()`, table, table)
		if _, err := s.db.DB.Exec(statement); err != nil {
			return fmt.Errorf("tracking PostgreSQL graph scope changes: %w", err)
		}
	}
	return nil
}

func (s *Store) lockCommitWatermark(tx *transaction) (int64, error) {
	query := `SELECT value FROM abom_commit_sequence WHERE singleton = 1`
	if s.db.postgres {
		query += " FOR UPDATE"
	}
	var sequence int64
	if err := tx.QueryRow(query).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("reading committed graph changes: %w", err)
	}
	return sequence, nil
}

func (s *Store) nextCommitWatermark(tx *transaction, reset bool) (int64, error) {
	if _, err := tx.Exec(`UPDATE abom_commit_sequence SET value = value + 1 WHERE singleton = 1`); err != nil {
		return 0, err
	}
	sequence, err := s.lockCommitWatermark(tx)
	if err != nil {
		return 0, err
	}
	if reset {
		if _, err := tx.Exec(`UPDATE abom_commit_sequence SET reset_at = value WHERE singleton = 1`); err != nil {
			return 0, err
		}
	}
	return sequence, nil
}

const changedNodeRows = `WITH changes AS MATERIALIZED (
	SELECT object_kind, object_id FROM abom_graph_change WHERE sequence > ?
)
SELECT object_id FROM changes WHERE object_kind = 'node'
UNION SELECT src_node_row_id FROM abom_edge_any_mode
	WHERE row_id IN (SELECT object_id FROM changes WHERE object_kind = 'edge')
UNION SELECT dst_node_row_id FROM abom_edge_any_mode
	WHERE row_id IN (SELECT object_id FROM changes WHERE object_kind = 'edge')
UNION SELECT src_node_row_id FROM abom_edge_sighting
	WHERE edge_row_id IN (SELECT object_id FROM changes WHERE object_kind = 'edge')
UNION SELECT dst_node_row_id FROM abom_edge_sighting
	WHERE edge_row_id IN (SELECT object_id FROM changes WHERE object_kind = 'edge')`

const changedEdgeSources = `WITH changes AS MATERIALIZED (
	SELECT object_id FROM abom_graph_change WHERE sequence > ? AND object_kind = 'edge'
)
SELECT src_node_row_id FROM abom_edge_any_mode WHERE row_id IN (SELECT object_id FROM changes)
UNION SELECT src_node_row_id FROM abom_edge_sighting WHERE edge_row_id IN (SELECT object_id FROM changes)`
