package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// pinsAt is an edges run of a repository at a commit seeing the line pins wrote name vendor/comp at
// ref.
func (f *releaseFixture) pinsAt(t *testing.T, repository, commit, ref string) {
	t.Helper()
	var repo int64
	if err := f.s.DB().QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = ?`,
		repository).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	run, err := f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "abom/edges", Repository: repository, DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.w.AddEdge(store.Edge{Key: repository + ":.github/workflows/ci.yml:9:9",
		SrcKey: repository + "-step", DstKey: "vendor/comp", Kind: store.EdgeKindStepUsesAction,
		RefRaw: "vendor/comp@" + ref, RefValue: ref, RepositoryID: repo, QueryRunID: run,
		FilePath: ".github/workflows/ci.yml", StartLine: 9, StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
}

// reportsOnLine is a findings run of a repository at a commit reporting a rule on the line pins
// wrote.
func (f *releaseFixture) reportsOnLine(t *testing.T, repository, commit, rule, identity string, score float64) {
	t.Helper()
	var repo int64
	if err := f.s.DB().QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = ?`,
		repository).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	run, err := f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "findings/suite.qls", Repository: repository, DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddFindings([]store.Finding{{QueryRunID: run, RuleID: rule, RepositoryID: repo,
		FilePath: ".github/workflows/ci.yml", StartLine: 9, StartColumn: 9, Severity: "warning",
		SecuritySeverity: &score, Fingerprint: identity, ResultHash: identity}}); err != nil {
		t.Fatal(err)
	}
}

// A version carries what any rule reported on the uses: lines naming it, among the repositories
// its row counts, at the commit each line was analyzed. An upstream repository's line is not
// counted unless upstream repositories are asked for, and a version whose lines no findings run
// reached is not analyzed rather than clean.
func TestAVersionCarriesTheFindingsOnTheLinesNamingIt(t *testing.T) {
	f := releasesInUse(t)
	f.pinsAt(t, "octo/app", "a1", "v1")
	f.reportsOnLine(t, "octo/app", "a1", "actions/unpinned-tag", "app-a1", 5)
	if _, err := f.s.InsertUpstreamRepository("third/party"); err != nil {
		t.Fatal(err)
	}
	f.pins(t, "third/party", "v1")
	f.pinsAt(t, "third/party", "t1", "v1")
	f.reportsOnLine(t, "third/party", "t1", "actions/unpinned-tag", "third-t1", 9.5)
	f.pinsAt(t, "octo/other", "o1", "v2")

	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})
	if v := versionNamed(t, page, "v1"); v.LineFindings == nil || v.LineFindings.Count != 1 ||
		v.LineFindings.Severity.Medium != 1 || v.LineFindings.Severity.Critical != 0 {
		t.Errorf("v1 carries %+v on its lines, want octo/app's one medium finding and not third/party's",
			v.LineFindings)
	}
	for _, ref := range []string{"v2", "v1.0.1", "v9"} {
		if v := versionNamed(t, page, ref); v.LineFindings != nil {
			t.Errorf("%s carries %+v; no findings run reached its lines, so it is not analyzed", ref, *v.LineFindings)
		}
	}

	asked := versionsOf(t, f.s, "vendor/comp", UsesQuery{Origins: []string{store.OriginUpstream}})
	if v := versionNamed(t, asked, "v1"); v.LineFindings == nil || v.LineFindings.Count != 1 ||
		v.LineFindings.Severity.Critical != 1 {
		t.Errorf("origins=upstream: v1 carries %+v on its lines, want third/party's one critical finding",
			v.LineFindings)
	}

	// A version no counted repository names on a line of its own has no lines to report on.
	for _, key := range []string{"vendor/comp", "actions/cache", "actions/setup-go"} {
		for _, v := range versionsOf(t, f.s, key, UsesQuery{}).Items {
			if v.Lines == 0 && v.LineFindings != nil {
				t.Errorf("%s@%s has no lines and carries %+v on them", key, v.Reference, *v.LineFindings)
			}
		}
	}
}
