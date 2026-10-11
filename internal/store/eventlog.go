package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// eventLogDDL is the receiver's durable log; row order is processing order.
const eventLogDDL = `
CREATE TABLE IF NOT EXISTS abom_event_delivery (
  row_id       INTEGER PRIMARY KEY,
  delivery_id  TEXT NOT NULL UNIQUE,
  event        TEXT NOT NULL,
  received_at  TEXT NOT NULL,
  payload      BLOB NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',
  reason       TEXT NOT NULL DEFAULT '',
  detail       TEXT NOT NULL DEFAULT '',
  attempts     INTEGER NOT NULL DEFAULT 0,
  processed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_event_delivery_status ON abom_event_delivery(status, row_id);
`

// Event delivery outcomes: pending until settled, then exactly one of the rest.
const (
	EventPending = "pending"
	EventDone    = "done"
	EventIgnored = "ignored"
	EventFailed  = "failed"
)

// EventDelivery is one webhook delivery as it arrived, and what processing it decided.
type EventDelivery struct {
	ID         int64  `json:"-"`
	DeliveryID string `json:"deliveryId"`
	Event      string `json:"event"`
	ReceivedAt string `json:"receivedAt"`
	Status     string `json:"status"`
	// Reason is a fixed phrase outcomes are counted by; Detail is this delivery's own account.
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
	Attempts    int    `json:"attempts"`
	ProcessedAt string `json:"processedAt"`
	Payload     []byte `json:"-"`
}

// EventOutcome is how many deliveries are in one state for one reason.
type EventOutcome struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// EventLog summarizes the delivery log.
type EventLog struct {
	Received int `json:"received"`
	// Since is when the oldest kept delivery arrived: every count here covers only what retention kept.
	Since *time.Time     `json:"since,omitempty"`
	Last  *EventDelivery `json:"last"`
	// Statuses holds every state, so a reader can say "none" rather than leave a gap.
	Statuses map[string]int  `json:"statuses"`
	Outcomes []EventOutcome  `json:"outcomes"`
	Failures []EventDelivery `json:"failures"`
}

const deliveryColumns = `row_id, delivery_id, event, received_at, status, reason, detail, attempts,
	COALESCE(processed_at, '')`

func scanDelivery(r interface{ Scan(...any) error }, extra ...any) (EventDelivery, error) {
	var d EventDelivery
	err := r.Scan(append([]any{&d.ID, &d.DeliveryID, &d.Event, &d.ReceivedAt, &d.Status,
		&d.Reason, &d.Detail, &d.Attempts, &d.ProcessedAt}, extra...)...)
	return d, err
}

// AppendDelivery records a delivery and reports whether it was new; a redelivery keeps the first.
func (s *Store) AppendDelivery(d EventDelivery) (bool, error) {
	if d.DeliveryID == "" || d.Event == "" {
		return false, fmt.Errorf("store: a delivery needs an id and an event")
	}
	if d.Payload == nil {
		d.Payload = []byte{}
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("recording delivery %s: %w", d.DeliveryID, err)
	}
	defer func() { _ = conn.Close() }()
	// The sender treats the return as safe, so the commit must survive power loss; NORMAL does not.
	synchronous, restore := `PRAGMA synchronous = FULL`, `PRAGMA synchronous = NORMAL`
	if s.db.postgres {
		synchronous, restore = `SET synchronous_commit = on`, `RESET synchronous_commit`
	}
	if _, err := conn.ExecContext(ctx, synchronous); err != nil {
		return false, fmt.Errorf("recording delivery %s: %w", d.DeliveryID, err)
	}
	// Left at FULL the connection only writes more slowly, which is no reason to refuse a delivery.
	defer func() { _, _ = conn.ExecContext(ctx, restore) }()

	res, err := conn.ExecContext(ctx, `
		INSERT INTO abom_event_delivery (delivery_id, event, received_at, payload)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(delivery_id) DO NOTHING`, d.DeliveryID, d.Event, now(), d.Payload)
	if err != nil {
		return false, fmt.Errorf("recording delivery %s: %w", d.DeliveryID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording delivery %s: %w", d.DeliveryID, err)
	}
	return n == 1, nil
}

// Delivery reads one delivery back by GitHub's delivery id.
func (s *Store) Delivery(deliveryID string) (EventDelivery, bool, error) {
	var payload []byte
	d, err := scanDelivery(s.db.QueryRow(`SELECT `+deliveryColumns+`, payload
		FROM abom_event_delivery WHERE delivery_id = ?`, deliveryID), &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return EventDelivery{}, false, nil
	}
	if err != nil {
		return EventDelivery{}, false, fmt.Errorf("reading delivery %s: %w", deliveryID, err)
	}
	d.Payload = payload
	return d, true, nil
}

// PendingDeliveries returns up to limit pending deliveries after row `after`, in recorded order.
func (s *Store) PendingDeliveries(after int64, limit int) ([]EventDelivery, error) {
	rows, err := s.db.Query(`SELECT `+deliveryColumns+`, payload FROM abom_event_delivery
		WHERE status = ? AND row_id > ? ORDER BY row_id LIMIT ?`, EventPending, after, limit)
	if err != nil {
		return nil, fmt.Errorf("reading pending deliveries: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (EventDelivery, error) {
		var payload []byte
		d, err := scanDelivery(rows, &payload)
		if err != nil {
			return EventDelivery{}, fmt.Errorf("scanning a pending delivery: %w", err)
		}
		d.Payload = payload
		return d, nil
	})
}

// SettleDelivery records a pending delivery's outcome once, and reports whether it did.
func (s *Store) SettleDelivery(id int64, status, reason, detail string) (bool, error) {
	if status != EventDone && status != EventIgnored && status != EventFailed {
		return false, fmt.Errorf("store: %q is not an outcome a delivery settles to", status)
	}
	res, err := s.db.Exec(`
		UPDATE abom_event_delivery
		   SET status = ?, reason = ?, detail = ?, processed_at = ?, attempts = attempts + 1
		 WHERE row_id = ? AND status = ?`, status, reason, detail, now(), id, EventPending)
	if err != nil {
		return false, fmt.Errorf("settling delivery %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeferDelivery leaves a delivery pending, counts the attempt and says why it is waiting.
func (s *Store) DeferDelivery(id int64, reason, detail string) error {
	if _, err := s.db.Exec(`
		UPDATE abom_event_delivery SET reason = ?, detail = ?, attempts = attempts + 1
		 WHERE row_id = ? AND status = ?`, reason, detail, id, EventPending); err != nil {
		return fmt.Errorf("deferring delivery %d: %w", id, err)
	}
	return nil
}

// EventLog summarizes arrivals, the latest, each state by reason, and up to `failures` failures.
func (s *Store) EventLog(failures int) (EventLog, error) {
	out := EventLog{
		Statuses: map[string]int{EventPending: 0, EventDone: 0, EventIgnored: 0, EventFailed: 0},
		Outcomes: []EventOutcome{},
		Failures: []EventDelivery{},
	}
	outcomes, err := queryAll(s, "reading delivery outcomes", func(r *sql.Rows) (EventOutcome, error) {
		var o EventOutcome
		return o, r.Scan(&o.Status, &o.Reason, &o.Count)
	}, `SELECT status, reason, COUNT(*) FROM abom_event_delivery
	     GROUP BY status, reason ORDER BY status, COUNT(*) DESC, reason`)
	if err != nil {
		return EventLog{}, err
	}
	for _, o := range outcomes {
		out.Outcomes = append(out.Outcomes, o)
		out.Statuses[o.Status] += o.Count
		out.Received += o.Count
	}

	last, err := scanDelivery(s.db.QueryRow(`SELECT ` + deliveryColumns +
		` FROM abom_event_delivery ORDER BY row_id DESC LIMIT 1`))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return EventLog{}, fmt.Errorf("reading the last delivery: %w", err)
	default:
		out.Last = &last
	}

	var oldest string
	err = s.db.QueryRow(`SELECT received_at FROM abom_event_delivery ORDER BY row_id LIMIT 1`).Scan(&oldest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return EventLog{}, fmt.Errorf("reading when the oldest kept delivery arrived: %w", err)
	default:
		since, err := time.Parse(time.RFC3339Nano, oldest)
		if err != nil {
			return EventLog{}, fmt.Errorf("reading when the oldest kept delivery arrived: %w", err)
		}
		out.Since = &since
	}

	failed, err := queryAll(s, "reading failed deliveries", func(r *sql.Rows) (EventDelivery, error) {
		return scanDelivery(r)
	}, `SELECT `+deliveryColumns+` FROM abom_event_delivery
	     WHERE status = ? ORDER BY row_id DESC LIMIT ?`, EventFailed, failures)
	if err != nil {
		return EventLog{}, err
	}
	out.Failures = append(out.Failures, failed...)
	return out, nil
}

// HistoryRetention outlasts GitHub's 3-day redelivery window and the recent outcomes an operator reads back, with room.
const HistoryRetention = 30 * 24 * time.Hour

// HistoryPruned counts what one prune removed.
type HistoryPruned struct {
	Deliveries int `json:"deliveries"`
	Buckets    int `json:"buckets"`
	Runs       int `json:"runs"`
}

// PruneHistory removes in one transaction what was settled, spent or recorded before `before`; an unprocessed delivery stays.
func (s *Store) PruneHistory(before time.Time) (HistoryPruned, error) {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return HistoryPruned{}, fmt.Errorf("pruning history before %s: %w", cutoff, err)
	}
	defer func() { _ = tx.Rollback() }()
	remove := func(what, query string, args ...any) (int, error) {
		res, err := tx.Exec(query, args...)
		if err != nil {
			return 0, fmt.Errorf("pruning %s before %s: %w", what, cutoff, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("pruning %s before %s: %w", what, cutoff, err)
		}
		return int(n), nil
	}

	var out HistoryPruned
	// Compared as times, not text: received_at carries a fraction of varying width, which sorts wrongly as text.
	if out.Deliveries, err = remove("deliveries", `DELETE FROM abom_event_delivery
		WHERE julianday(received_at) < julianday(?) AND status <> ?`, cutoff, EventPending); err != nil {
		return HistoryPruned{}, err
	}
	if out.Buckets, err = remove("meter minutes", `DELETE FROM abom_pool_spend
		WHERE julianday(bucket) < julianday(?)`, cutoff); err != nil {
		return HistoryPruned{}, err
	}
	if out.Runs, err = remove("workflow runs", `DELETE FROM abom_workflow_run
		WHERE julianday(recorded_at) < julianday(?)`, cutoff); err != nil {
		return HistoryPruned{}, err
	}
	if err := tx.Commit(); err != nil {
		return HistoryPruned{}, fmt.Errorf("pruning history before %s: %w", cutoff, err)
	}
	return out, nil
}

// QueueRow is one row of the analysis queue as recorded, which is what a push is judged against.
type QueueRow struct {
	NodeKey     string
	Ref         string
	Owner       string
	Repo        string
	Status      string
	Depth       int
	ResolvedSHA string
	Reason      string
	CheckedAt   string
	Expedited   bool
	// NextAttemptAt is when the row may next be claimed: a retry's backoff, or a hold's ceiling.
	NextAttemptAt string
}

// queueRowColumns and scanQueueRow read a QueueRow, so a single row and a reference's rows agree.
const queueRowColumns = `node_key, ref, COALESCE(owner, ''), COALESCE(repo, ''), status, depth,
	COALESCE(resolved_sha, ''), COALESCE(reason, ''), COALESCE(checked_at, ''), expedited_at IS NOT NULL,
	COALESCE(next_attempt_at, '')`

func scanQueueRow(row interface{ Scan(...any) error }) (QueueRow, error) {
	var r QueueRow
	err := row.Scan(&r.NodeKey, &r.Ref, &r.Owner, &r.Repo, &r.Status, &r.Depth, &r.ResolvedSHA,
		&r.Reason, &r.CheckedAt, &r.Expedited, &r.NextAttemptAt)
	return r, err
}

// QueueRow reads one row of the analysis queue, and whether it exists.
func (s *Store) QueueRow(nodeKey, ref string) (QueueRow, bool, error) {
	r, err := scanQueueRow(s.db.QueryRow(
		`SELECT `+queueRowColumns+` FROM abom_frontier WHERE node_key = ? AND ref = ?`, nodeKey, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return QueueRow{}, false, nil
	}
	if err != nil {
		return QueueRow{}, false, fmt.Errorf("reading the queue row %s@%s: %w", nodeKey, ref, err)
	}
	return r, true, nil
}

// QueueRowsAt reads every queue row standing at one reference of a repository, whatever path and
// depth: each action of it that some workflow uses at that reference.
func (s *Store) QueueRowsAt(fullName, ref string) ([]QueueRow, error) {
	owner, repo, _ := strings.Cut(fullName, "/")
	return queryAll(s, "reading the queue rows at "+fullName+"@"+ref,
		func(r *sql.Rows) (QueueRow, error) { return scanQueueRow(r) },
		`SELECT `+queueRowColumns+` FROM abom_frontier
		  WHERE LOWER(owner) = LOWER(?) AND LOWER(repo) = LOWER(?) AND ref = ?
		  ORDER BY depth, node_key`, owner, repo, ref)
}
