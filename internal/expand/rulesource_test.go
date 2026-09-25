package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func tracesOf(t *testing.T, s *store.Store, where string) map[string]store.FindingTrace {
	t.Helper()
	rows, err := s.DB().Query(`SELECT f.row_id FROM abom_finding f
		JOIN repository r ON r.row_id = f.repository_row_id WHERE ` + where)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	out := map[string]store.FindingTrace{}
	for _, id := range ids {
		trace, err := s.FindingTrace(id)
		if err != nil {
			t.Fatalf("FindingTrace: %v", err)
		}
		out[trace.RuleID] = trace
	}
	return out
}

// A finding the crawl stored is traced to the rule its lane ran: the version and pack of that
// rule's own query, not the lane's combined hash, and the crawl as what recorded it. A rule the
// lane did not name is left unknown rather than given the lane's pack.
func TestACrawledFindingIsTracedToTheRuleItsLaneRan(t *testing.T) {
	h, _ := findingsHarness(t)
	h.enqueue(item("actions", "checkout", "", 1))
	runBound(t, h)

	version, err := runner.QueryVersion(h.laneFindings)
	if err != nil {
		t.Fatalf("QueryVersion: %v", err)
	}
	traces := tracesOf(t, h.store, "1")
	named, unnamed := traces["actions/unpinned-tag"], traces["actions/unnecessary-use-of-advanced-config"]
	if len(named.Evaluations) != 1 || len(unnamed.Evaluations) != 1 {
		t.Fatalf("the recorded SARIF's two rules trace to %+v and %+v", named.Evaluations, unnamed.Evaluations)
	}
	if e := named.Evaluations[0]; e.RecordedBy != store.RecordedByCrawl || e.Source != store.QuerySourceUpstream ||
		e.RuleVersion != version || e.Pack != "codeql/actions-queries" || e.PackVersion != "0.6.34" ||
		e.PackDigest == "" {
		t.Errorf("the lane's own rule traces to %+v, want the crawl and the query at version %s", e, version)
	}
	if e := unnamed.Evaluations[0]; e.RecordedBy != store.RecordedByCrawl || e.Source != "" || e.RuleVersion != "" {
		t.Errorf("a rule the lane did not name traces to %+v, want its source and version unknown", e)
	}

	var unsaid int
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_run
		WHERE COALESCE(recorded_by, '') <> ?`, store.RecordedByCrawl).Scan(&unsaid); err != nil {
		t.Fatal(err)
	}
	if unsaid != 0 {
		t.Errorf("%d evaluations the crawl recorded do not say the crawl recorded them", unsaid)
	}
}

// A tier bound to two suites runs one lane over the rules of both. Folding the plans has to carry
// every field, and one it drops is a rule whose findings no evaluation can account for.
func TestATierOfTwoSuitesKeepsTheRulesOfBoth(t *testing.T) {
	var merged tierPlan
	merged.absorb(tierPlan{findingsRules: []store.QueryRunRule{{RuleID: "actions/a", Pack: "codeql/actions-queries"}}})
	merged.absorb(tierPlan{findingsRules: []store.QueryRunRule{{RuleID: "abom/b", Pack: "advanced-security/abom-x"}}})

	run := merged.findingsRun(revision{repository: "octo/app", commit: "c1"})
	if len(run.Rules) != 2 || run.Rules[0].RuleID != "actions/a" || run.Rules[1].RuleID != "abom/b" {
		t.Errorf("the tier's lane runs %+v, want the rules of both suites", run.Rules)
	}
}

// A finding a run stored is traced to that run and to the version of the query it asked for, so
// it is told apart from the same rule the crawl ran.
func TestARunsFindingIsTracedToTheQueryVersionItAskedFor(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	version, err := runner.QueryVersion(e.Resolver.(*fakeResolver).queries[0])
	if err != nil {
		t.Fatalf("QueryVersion: %v", err)
	}

	asked := tracesOf(t, s, "r.repository_full_name = 'octo-app'")["actions/unpinned-tag"]
	if len(asked.Evaluations) != 1 {
		t.Fatalf("the run's finding traces to %+v", asked.Evaluations)
	}
	ev := asked.Evaluations[0]
	if ev.RecordedBy != store.RecordedByRun || ev.Source != store.QuerySourceUpstream || ev.RuleVersion != version ||
		ev.Pack != "codeql/actions-queries" || ev.PackVersion != "0.6.35" {
		t.Errorf("the run's finding traces to %+v, want a run of the query at version %s", ev, version)
	}
	if len(ev.Runs) != 1 || ev.Runs[0].RunID != id || ev.Runs[0].Repository != "octo-app" {
		t.Errorf("the evaluation names runs %+v, want run %d's octo-app member", ev.Runs, id)
	}
}
