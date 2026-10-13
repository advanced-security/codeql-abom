package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// demoDatasetDDL marks a database as a Demo instance's practice data. At most one row, written once.
const demoDatasetDDL = `
CREATE TABLE IF NOT EXISTS abom_demo_dataset (
  singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
  dataset_id TEXT NOT NULL CHECK (dataset_id <> ''),
  scenario   TEXT NOT NULL CHECK (scenario <> ''),
  marked_at  TEXT NOT NULL
);`

// ErrDemoDatabase is an ordinary open of a database marked as Demo practice data.
var ErrDemoDatabase = errors.New("this database holds Demo practice data; " +
	"only `codeql-abom demo serve` opens it, and an ordinary setup needs its own database")

// DemoDataset identifies the practice data a Demo instance serves.
type DemoDataset struct {
	ID       string `json:"id"`
	Scenario string `json:"scenario"`
	MarkedAt string `json:"markedAt"`
}

// MarkDemo records that this database is Demo practice data. A database is marked once.
func (s *Store) MarkDemo(d DemoDataset) error {
	if d.ID == "" || d.Scenario == "" {
		return errors.New("a demo marker needs a dataset identity and a scenario version")
	}
	res, err := s.db.Exec(`
INSERT INTO abom_demo_dataset (singleton, dataset_id, scenario, marked_at) VALUES (1, ?, ?, ?)
ON CONFLICT(singleton) DO NOTHING`,
		d.ID, d.Scenario, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("marking the demo dataset: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errors.New("this database is already marked as a demo dataset")
	}
	return nil
}

// DemoDataset reports the marker, and false for an ordinary database.
func (s *Store) DemoDataset() (DemoDataset, bool, error) {
	if ok, err := s.hasDemoTable(); err != nil || !ok {
		return DemoDataset{}, false, err
	}
	var d DemoDataset
	err := s.db.QueryRow(`SELECT dataset_id, scenario, marked_at FROM abom_demo_dataset WHERE singleton = 1`).
		Scan(&d.ID, &d.Scenario, &d.MarkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DemoDataset{}, false, nil
	}
	if err != nil {
		return DemoDataset{}, false, fmt.Errorf("reading the demo marker: %w", err)
	}
	return d, true, nil
}

// hasDemoTable is asked before migrating, when an older database has no such table.
func (s *Store) hasDemoTable() (bool, error) {
	var ok bool
	q := `SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'abom_demo_dataset'`
	if s.db.postgres {
		q = `SELECT to_regclass('abom_demo_dataset') IS NOT NULL`
	}
	if err := s.db.DB.QueryRow(q).Scan(&ok); err != nil {
		return false, fmt.Errorf("checking for a demo marker: %w", err)
	}
	return ok, nil
}

// refuseDemo keeps every ordinary command away from practice data, whichever subcommand opened it.
func (s *Store) refuseDemo(opts Options) error {
	if opts.Demo {
		return nil
	}
	_, marked, err := s.DemoDataset()
	if err != nil {
		return err
	}
	if marked {
		return ErrDemoDatabase
	}
	return nil
}
