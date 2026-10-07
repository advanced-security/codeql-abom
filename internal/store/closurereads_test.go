package store

import (
	"fmt"
	"testing"
)

// closureOverARegularAnalysis is the case a closure's before-and-after diff on the crawled
// organization found: an owned repository the crawl analyzed at one commit, under an older CodeQL
// and before rules were kept, and then a closure of that repository at that same commit under the
// CodeQL running now, which keeps them. A closure run is named after its root at the root's
// commit, so a reader joining runs by repository and commit meets it unless it names a mode.
func closureOverARegularAnalysis(t *testing.T) (*Store, func()) {
	t.Helper()
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
		enqueued_at, resolved_sha, analyzed_at) VALUES ('octo/app', '', 'octo', 'app', 0, 'done',
		'2026-09-01T00:00:00Z', 'c1', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	regular, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom-findings.qls", Repository: "octo/app", DBCommit: "c1", CLIVersion: "2.27.0",
		Pack: "advanced-security/abom-actions-findings-src", PackDigest: "regular-digest",
		AnalysisMode: AnalysisModeRegular, RecordedBy: RecordedByCrawl,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: app, Path: ".github/workflows/ci.yml"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	finding := Finding{Provider: "CodeQL", RuleID: "actions/unpinned-tag", RepositoryID: app,
		FilePath: ".github/workflows/ci.yml", StartLine: 7, Fingerprint: "fp"}
	finding.QueryRunID = regular
	if _, err := s.AddFindings([]Finding{finding}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatal(err)
	}
	return s, func() {
		t.Helper()
		run, err := s.RecordQueryRun(QueryRun{
			QueryID: "abom-findings.qls", Repository: "octo/app", DBCommit: "c1",
			CLIVersion: "2.27.1", Pack: "advanced-security/abom-actions-findings-src",
			PackDigest: "closure-digest", AnalysisMode: AnalysisModeExtended,
			RecordedBy: RecordedByClosure, Closure: "digest",
			Rules: []QueryRunRule{{RuleID: "actions/unpinned-tag", Pack: "codeql/actions-queries",
				PackVersion: "0.6.36", PackDigest: "closure-digest"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		finding.QueryRunID = run
		if _, err := s.AddFindings([]Finding{finding}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LinkFindingsToNodes(); err != nil {
			t.Fatal(err)
		}
	}
}

// What the crawl ran at a queue row, where a rule's query came from, and what the catalog says
// has run are regular facts. A closure run of the same repository at the same commit is not an
// analysis of that row, so none of them may answer differently once it exists.
func TestAClosureRunMovesNoReaderOfTheRegularLedger(t *testing.T) {
	s, closure := closureOverARegularAnalysis(t)
	read := func() string {
		t.Helper()
		frontier, err := s.FrontierSnapshot("", 0)
		if err != nil {
			t.Fatal(err)
		}
		repository, err := s.RepositoryDetailOf("octo/app")
		if err != nil {
			t.Fatal(err)
		}
		node, err := s.NodeDetailOf("octo/app/.github/workflows/ci.yml", nil)
		if err != nil {
			t.Fatal(err)
		}
		bySource, err := s.NodesMatching(FindingSelector{Rules: []string{"actions/unpinned-tag"},
			Sources: []string{QuerySourceUpstream}})
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := s.QueryCatalog()
		if err != nil {
			t.Fatal(err)
		}
		runs := []string{}
		for _, q := range catalog {
			runs = append(runs, fmt.Sprintf("%s %s runs=%d repositories=%d cli=%s last=%s",
				q.QueryID, q.Pack, q.Runs, q.Repositories, q.CLIVersion, q.LastRunAt))
		}
		return fmt.Sprintf("frontier cli=%q suites=%v\nrepository rules=%+v\nnode rules=%+v\n"+
			"matched by source=%d unsourced=%d\ncatalog=%v",
			frontier[0].CLIVersion, frontier[0].Suites, repository.FindingsByRule, node.Rules,
			len(bySource.Nodes), bySource.Unsourced, runs)
	}
	before := read()
	closure()
	if after := read(); after != before {
		t.Errorf("a closure run moved a regular reader:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// After a CodeQL upgrade the crawl requeues what it analyzed under the old one. A closure run at
// the new CodeQL is not that analysis, so it must not make its root look current and leave the
// root's own findings on the old version.
func TestAClosureRunAtTheNewCodeQLDoesNotMakeItsRootLookCurrent(t *testing.T) {
	s, closure := closureOverARegularAnalysis(t)
	closure()
	requeued, err := s.RequeueStale("2.27.1", []string{"closure-digest"})
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if requeued != 1 {
		t.Errorf("requeued %d, want the root, whose own analysis ran under 2.27.0", requeued)
	}
}

// A defect in a dependency seen through a closure is the dependency's, and the consumer has the
// exposure. Its trace has to say which owned repository's closure reached it, or an extended
// sighting reads as one more report of the dependency's own analysis.
func TestATraceNamesTheOwnedRepositoryAClosureReachedAFindingThrough(t *testing.T) {
	s := newTestStore(t)
	comp, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	finding := Finding{Provider: "CodeQL", RuleID: "actions/unpinned-tag", RepositoryID: comp,
		FilePath: "action.yml", StartLine: 7, Fingerprint: "fp"}
	for _, run := range []QueryRun{
		{QueryID: "abom-findings.qls", Repository: "vendor/comp", DBCommit: "c1",
			AnalysisMode: AnalysisModeRegular, RecordedBy: RecordedByCrawl},
		{QueryID: "abom-findings.qls", Repository: "octo/app", DBCommit: "a1",
			AnalysisMode: AnalysisModeExtended, RecordedBy: RecordedByClosure, Closure: "digest"},
	} {
		id, err := s.RecordQueryRun(run)
		if err != nil {
			t.Fatal(err)
		}
		finding.QueryRunID = id
		if _, err := s.AddFindings([]Finding{finding}); err != nil {
			t.Fatal(err)
		}
	}
	var row int64
	if err := s.db.QueryRow(`SELECT row_id FROM abom_finding`).Scan(&row); err != nil {
		t.Fatal(err)
	}
	trace, err := s.FindingTrace(row)
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	got := map[string]string{}
	for _, e := range trace.Evaluations {
		got[e.RecordedBy] = e.Repository + "@" + e.Commit + " " + e.Mode
	}
	want := map[string]string{
		RecordedByCrawl:   "vendor/comp@c1 regular",
		RecordedByClosure: "octo/app@a1 extended",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("evaluations %v, want %v", got, want)
	}
}
