package expand

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// frontierRows is every frontier row, every column, as text: a run that changed any of them wrote
// to the crawl's queue.
func frontierRows(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT * FROM abom_frontier ORDER BY 1`)
	if err != nil {
		t.Fatalf("reading the frontier: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading the frontier's columns: %v", err)
	}
	out := []string{}
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		into := make([]any, len(columns))
		for i := range values {
			into[i] = &values[i]
		}
		if err := rows.Scan(into...); err != nil {
			t.Fatalf("scanning the frontier: %v", err)
		}
		cells := make([]string, len(values))
		for i, v := range values {
			cells[i] = columns[i] + "=" + v.String
		}
		out = append(out, strings.Join(cells, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the frontier: %v", err)
	}
	return out
}

// A run is a reader with a different plan, not a second writer to the queue. Finished or stopped
// mid-build, it leaves every frontier row as it was — including the row for the repository it
// fetched and built — so the crawl resumes exactly where it would have.
func TestARunLeavesTheFrontierExactlyAsItWas(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		h, e, id, _ := executorHarness(t)
		delete(h.fetchErr, "octo/absent")
		h.enqueue(item("octo", "absent", "", 0), item("octo", "elsewhere", "", 1))
		before := frontierRows(t, h.store)
		if len(before) == 0 {
			t.Fatal("the fixture queued nothing; the comparison proves nothing")
		}

		ctx, cancel := context.WithCancel(context.Background())
		if stopped {
			h.expander.Builder = cancelingBuilder{cancel}
		}
		err := e.Run(ctx, id)
		cancel()
		if stopped != (err != nil) {
			t.Fatalf("stopped=%v: Run returned %v", stopped, err)
		}
		if !stopped {
			if m := membersByRepository(t, h.store, id)["octo/absent"]; m.Status != store.AnalysisSucceeded {
				t.Fatalf("the member the run builds is %q; the fixture never reached the build", m.Status)
			}
		}

		if after := frontierRows(t, h.store); !slices.Equal(before, after) {
			t.Errorf("stopped=%v: the run moved the frontier\nbefore %v\nafter  %v", stopped, before, after)
		}
	}
}
