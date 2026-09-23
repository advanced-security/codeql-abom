package store

import "fmt"

// InterruptAbandonedRuns interrupts runs still marked running; valid only before serving starts.
func (s *Store) InterruptAbandonedRuns() (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE abom_analysis_member SET status = ?, started_at = NULL,
		       detail = 'the server stopped while this was being analyzed'
		WHERE status = ? AND run_id IN (SELECT row_id FROM abom_analysis_run WHERE state = ?)`,
		AnalysisPending, AnalysisRunning, RunRunning); err != nil {
		return 0, fmt.Errorf("returning interrupted members to pending: %w", err)
	}
	res, err := tx.Exec(`UPDATE abom_analysis_run SET state = ? WHERE state = ?`,
		RunInterrupted, RunRunning)
	if err != nil {
		return 0, fmt.Errorf("marking abandoned runs interrupted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
