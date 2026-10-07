package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// appInstallationDDL keeps a vanished installation's last reading, marked unknown: revoked and unreadable look alike.
const appInstallationDDL = `
CREATE TABLE IF NOT EXISTS abom_app_installation_read (
  id           INTEGER PRIMARY KEY CHECK (id = 1),
  app_id       INTEGER NOT NULL,
  attempted_at TEXT NOT NULL,
  succeeded_at TEXT,
  unread       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS abom_app_installation (
  owner                TEXT NOT NULL PRIMARY KEY COLLATE NOCASE,
  kind                 TEXT NOT NULL DEFAULT '',
  installation_id      INTEGER NOT NULL,
  repository_selection TEXT NOT NULL DEFAULT '',
  state                TEXT NOT NULL,
  reason               TEXT NOT NULL DEFAULT '',
  detail               TEXT NOT NULL DEFAULT '',
  seen_at              TEXT NOT NULL,
  checked_at           TEXT NOT NULL,
  repositories_read_at TEXT
);

CREATE TABLE IF NOT EXISTS abom_app_installation_repository (
  owner TEXT NOT NULL COLLATE NOCASE,
  name  TEXT NOT NULL COLLATE NOCASE,
  PRIMARY KEY (owner, name)
);
`

// Installation evidence states: losing the App says nothing about ownership, so all but one are unknown.
const (
	InstallationInstalled = "installed"
	InstallationUnknown   = "unknown"
)

// Reasons an account's installation evidence is unknown. Fixed phrases, so they can be counted.
const (
	InstallationNotInstalled        = "not installed"
	InstallationNoLongerInstalled   = "no longer installed"
	InstallationSuspended           = "suspended"
	InstallationListUnread          = "the App's installations could not be read"
	InstallationRepositoriesUnread  = "its repositories could not be read"
	InstallationEvidenceNotReadable = "installation evidence could not be read"
)

// AppInstallation is one installation as the App's listing reported it.
type AppInstallation struct {
	Owner               string
	Kind                string
	InstallationID      int64
	RepositorySelection string
	// Repositories names what a selected installation may read. Unused when the selection is all.
	Repositories []string
	Suspended    bool
	// RepositoriesUnread says why a selected installation's repositories could not be read.
	RepositoriesUnread string
}

// InstallationSync is one attempt to read the App's installations.
type InstallationSync struct {
	AppID int64
	At    time.Time
	// Unread says why the listing could not be read, in which case Installations means nothing.
	Unread        string
	Installations []AppInstallation
}

// InstallationEvidence is what the App's installations say about one account.
type InstallationEvidence struct {
	State               string  `json:"state"`
	Reason              string  `json:"reason"`
	Detail              string  `json:"detail"`
	InstallationID      *int64  `json:"installationId"`
	RepositorySelection *string `json:"repositorySelection"`
	// Repositories is null unless a selected installation's list was read; then [] means none.
	Repositories       []string `json:"repositories"`
	RepositoriesReadAt *string  `json:"repositoriesReadAt"`
	// SeenAt is the last listing that named this account; CheckedAt the last read that concerned it.
	SeenAt    *string `json:"seenAt"`
	CheckedAt string  `json:"checkedAt"`
}

// RecordInstallationSync records one read of the App's installations.
func (s *Store) RecordInstallationSync(sync InstallationSync) error {
	at := sync.At.UTC().Format(time.RFC3339Nano)
	if sync.At.IsZero() {
		at = now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording the App's installations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if sync.Unread != "" {
		if _, err := tx.Exec(`
			INSERT INTO abom_app_installation_read (id, app_id, attempted_at, unread) VALUES (1, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			  app_id = excluded.app_id, attempted_at = excluded.attempted_at, unread = excluded.unread`,
			sync.AppID, at, sync.Unread); err != nil {
			return fmt.Errorf("recording the App's installations: %w", err)
		}
		if _, err := tx.Exec(`UPDATE abom_app_installation
			SET state = ?, reason = ?, detail = ?, checked_at = ?`,
			InstallationUnknown, InstallationListUnread, sync.Unread, at); err != nil {
			return fmt.Errorf("recording the App's installations: %w", err)
		}
		return tx.Commit()
	}

	if _, err := tx.Exec(`
		INSERT INTO abom_app_installation_read (id, app_id, attempted_at, succeeded_at, unread)
		VALUES (1, ?, ?, ?, '')
		ON CONFLICT(id) DO UPDATE SET app_id = excluded.app_id, attempted_at = excluded.attempted_at,
		  succeeded_at = excluded.succeeded_at, unread = ''`, sync.AppID, at, at); err != nil {
		return fmt.Errorf("recording the App's installations: %w", err)
	}
	for _, inst := range sync.Installations {
		if err := recordInstallation(tx, inst, at); err != nil {
			return fmt.Errorf("recording the installation on %s: %w", inst.Owner, err)
		}
	}
	// Every listed row was stamped with this read, so an older stamp is one the listing omitted.
	if _, err := tx.Exec(`UPDATE abom_app_installation
		SET state = ?, reason = ?, detail = '', checked_at = ? WHERE checked_at <> ?`,
		InstallationUnknown, InstallationNoLongerInstalled, at, at); err != nil {
		return fmt.Errorf("recording the App's installations: %w", err)
	}
	return tx.Commit()
}

func recordInstallation(tx *sql.Tx, inst AppInstallation, at string) error {
	state, reason, detail := InstallationInstalled, "", ""
	listRead := inst.RepositorySelection == "selected" && inst.RepositoriesUnread == "" && !inst.Suspended
	switch {
	case inst.Suspended:
		state, reason = InstallationUnknown, InstallationSuspended
	case inst.RepositorySelection == "selected" && inst.RepositoriesUnread != "":
		state, reason, detail = InstallationUnknown, InstallationRepositoriesUnread, inst.RepositoriesUnread
	}
	if _, err := tx.Exec(`
		INSERT INTO abom_app_installation (owner, kind, installation_id, repository_selection,
		  state, reason, detail, seen_at, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner) DO UPDATE SET kind = excluded.kind,
		  installation_id = excluded.installation_id,
		  repository_selection = excluded.repository_selection, state = excluded.state,
		  reason = excluded.reason, detail = excluded.detail, seen_at = excluded.seen_at,
		  checked_at = excluded.checked_at`,
		inst.Owner, inst.Kind, inst.InstallationID, inst.RepositorySelection,
		state, reason, detail, at, at); err != nil {
		return err
	}
	// A list that was not read keeps the last one that was; "all" has no list to keep.
	if !listRead && inst.RepositorySelection == "selected" {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM abom_app_installation_repository WHERE owner = ?`,
		inst.Owner); err != nil {
		return err
	}
	if !listRead {
		_, err := tx.Exec(`UPDATE abom_app_installation SET repositories_read_at = NULL
			WHERE owner = ?`, inst.Owner)
		return err
	}
	for _, name := range inst.Repositories {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO abom_app_installation_repository (owner, name)
			VALUES (?, ?)`, inst.Owner, name); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE abom_app_installation SET repositories_read_at = ? WHERE owner = ?`,
		at, inst.Owner)
	return err
}

// InstallationEvidence is keyed by lowercased owner, and empty when no read was ever attempted.
func (s *Store) InstallationEvidence(owners []string) (map[string]*InstallationEvidence, error) {
	out := map[string]*InstallationEvidence{}
	var attempted string
	var succeeded sql.NullString
	var unread string
	err := s.db.QueryRow(`SELECT attempted_at, succeeded_at, unread
		FROM abom_app_installation_read WHERE id = 1`).Scan(&attempted, &succeeded, &unread)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading installation evidence: %w", err)
	}
	for _, owner := range owners {
		e, err := s.installationRow(owner)
		if err != nil {
			return nil, err
		}
		if e == nil {
			e = &InstallationEvidence{State: InstallationUnknown, Reason: InstallationNotInstalled,
				CheckedAt: attempted}
			if unread != "" {
				e.Reason, e.Detail = InstallationListUnread, unread
			}
		}
		out[strings.ToLower(owner)] = e
	}
	return out, nil
}

func (s *Store) installationRow(owner string) (*InstallationEvidence, error) {
	var e InstallationEvidence
	var id int64
	var selection, seen string
	var listed sql.NullString
	err := s.db.QueryRow(`SELECT state, reason, detail, installation_id, repository_selection,
		  seen_at, checked_at, repositories_read_at
		FROM abom_app_installation WHERE owner = ?`, owner).
		Scan(&e.State, &e.Reason, &e.Detail, &id, &selection, &seen, &e.CheckedAt, &listed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the installation on %s: %w", owner, err)
	}
	e.InstallationID, e.RepositorySelection, e.SeenAt = &id, &selection, &seen
	if !listed.Valid {
		return &e, nil
	}
	e.RepositoriesReadAt = &listed.String
	rows, err := s.db.Query(`SELECT name FROM abom_app_installation_repository WHERE owner = ?`, owner)
	if err != nil {
		return nil, fmt.Errorf("reading the repositories installed on %s: %w", owner, err)
	}
	defer rows.Close()
	e.Repositories = []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		e.Repositories = append(e.Repositories, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(e.Repositories)
	return &e, nil
}
