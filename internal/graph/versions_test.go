package graph

import (
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// versionsOf answers the versions table for one node, failing the test on error.
func versionsOf(t *testing.T, s *store.Store, key string, q UsesQuery) VersionsPage {
	t.Helper()
	page, err := (&Builder{Store: s}).VersionsOf(key, q)
	if err != nil {
		t.Fatalf("VersionsOf(%q): %v", key, err)
	}
	return page
}

func versionNamed(t *testing.T, page VersionsPage, ref string) Version {
	t.Helper()
	for _, v := range page.Items {
		if v.Reference == ref {
			return v
		}
	}
	t.Fatalf("no version %q among %+v", ref, page.Items)
	return Version{}
}

// findingAt records a finding in vendor/comp's own code, reported by a findings run at a commit,
// and links it to the action the way attribution does.
func (f *releaseFixture) findingAt(t *testing.T, commit, rule, nodeKey, hash string) {
	t.Helper()
	run, err := f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "findings/suite.qls", Repository: "vendor/comp", DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddFindings([]store.Finding{{QueryRunID: run, RuleID: rule,
		RepositoryID: f.comp, FilePath: "action.yml", StartLine: 1, Severity: "error",
		ResultHash: hash, Fingerprint: hash}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB().Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
		 WHERE f.result_hash = ? AND n.node_key = ?`, hash, nodeKey); err != nil {
		t.Fatal(err)
	}
}

// A local path (`uses: ./`) names no version: it runs whatever commit its caller is at. Listed, it
// read as the default branch, which no workflow wrote; counted, the inventory row it opens under
// disagreed with it.
func TestALocalPathIsNotAVersionInUse(t *testing.T) {
	f := releasesInUse(t)
	f.pins(t, "vendor/self", "")
	b := &Builder{Store: f.s}
	if err := b.BuildBlastRadius(); err != nil {
		t.Fatal(err)
	}

	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})
	for _, v := range page.Items {
		if v.Reference == "" {
			t.Errorf("a local path is listed as a version: %+v", v)
		}
	}
	if page.Total != 4 || storedReferences(t, f.s, "vendor/comp") != 4 {
		t.Errorf("%d references in use, %d stored; want the 4 written",
			page.Total, storedReferences(t, f.s, "vendor/comp"))
	}
}

// A version only an upstream repository uses is nobody's here to manage. It is not listed or
// counted, in the table or in the inventory row it opens under, and the page says how many it left
// out rather than counting the repositories that use them.
func TestAVersionOnlyUpstreamRepositoriesUseIsLeftOutAndSaidSo(t *testing.T) {
	f := releasesInUse(t)
	upstream, err := f.s.InsertUpstreamRepository("third/party")
	if err != nil {
		t.Fatal(err)
	}
	workflow := "third/party/.github/workflows/ci.yml"
	f.node(t, store.Node{Key: workflow, Kind: "workflow", RepositoryID: upstream})
	f.node(t, store.Node{Key: "third-step", Kind: "step", RepositoryID: upstream, ContainerKey: workflow})
	if err := f.w.AddEdge(store.Edge{Key: "third/party:ci.yml:3:3", SrcKey: "third-step",
		DstKey: "vendor/comp", Kind: store.EdgeKindStepUsesAction, RefValue: "v7",
		RepositoryID: upstream, FilePath: ".github/workflows/ci.yml", StartLine: 3}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if err := (&Builder{Store: f.s}).BuildBlastRadius(); err != nil {
		t.Fatal(err)
	}

	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})
	for _, v := range page.Items {
		if v.Reference == "v7" {
			t.Errorf("v7 is listed, though only an upstream repository uses it: %+v", v)
		}
	}
	if page.Total != 4 || page.Unlisted != 1 || storedReferences(t, f.s, "vendor/comp") != 4 {
		t.Errorf("%d in use, %d left out, %d stored; want the 4 starting points use and v7 left out",
			page.Total, page.Unlisted, storedReferences(t, f.s, "vendor/comp"))
	}
	asked := versionsOf(t, f.s, "vendor/comp", UsesQuery{Origins: []string{store.OriginUpstream}})
	if v := versionNamed(t, asked, "v7"); v.Direct != 1 {
		t.Errorf("origins=upstream counts v7 as %+v, want third/party directly", v)
	}
}

// One row per reference in use, each saying what it resolves to now and who runs it by that
// reference, directly or through something they name. A reference nothing resolved is one ABOM has
// no reading of, and says so rather than borrowing a commit.
func TestEachVersionSaysWhatItResolvesToAndWhoRunsIt(t *testing.T) {
	f := releasesInUse(t)
	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})

	for ref, want := range map[string]struct {
		commit, queue string
		direct        int
	}{
		"v1": {"c1", "analyzed", 1}, "v2": {"c2", "analyzed", 1},
		"v1.0.1": {"c1-rebuilt", "analyzed", 1}, "v9": {"", "", 1},
	} {
		v := versionNamed(t, page, ref)
		if v.Commit != want.commit || v.Queue != want.queue || v.Direct != want.direct || v.Transitive != 0 {
			t.Errorf("%s: %+v, want commit %q, queue %q, %d direct", ref, v, want.commit, want.queue, want.direct)
		}
	}
	if page.Total != 4 || len(page.Items) != 4 {
		t.Errorf("%d versions of %d in use, want all 4", len(page.Items), page.Total)
	}
	cache := versionNamed(t, versionsOf(t, f.s, "actions/cache", UsesQuery{}), "v3")
	if cache.Direct != 0 || cache.Transitive != 2 {
		t.Errorf("actions/cache@v3: %+v, want two repositories through vendor/comp and none directly", cache)
	}
}

// A version's findings are what a findings run reported at the commit it resolves to — or at a
// commit settled as identical to it — never the action's findings at every commit added up. A
// version nothing ran the findings at is not analyzed, which is not zero.
func TestAVersionsFindingsAreThoseReportedAtItsCommit(t *testing.T) {
	f := releasesInUse(t)
	f.findingAt(t, "c1", "actions/code-injection/critical", "vendor/comp", "h-c1")
	f.findingAt(t, "c2", "actions/code-injection/critical", "vendor/comp", "h-c2a")
	f.findingAt(t, "c2", "actions/untrusted-checkout/high", "vendor/comp", "h-c2b")

	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})
	for ref, want := range map[string]int{"v1": 1, "v2": 2, "v1.0.1": 1} {
		v := versionNamed(t, page, ref)
		if v.Findings == nil || v.Findings.Count != want {
			t.Errorf("%s reports findings %+v, want %d", ref, v.Findings, want)
		}
	}
	if v := versionNamed(t, page, "v9"); v.Findings != nil {
		t.Errorf("v9 resolves to nothing and reports findings %+v; it is not analyzed", *v.Findings)
	}
}

// The advisories naming an action travel once, verbatim, for a reader to compare with the
// references. A finding on a calling step is not a version's verdict: steps are tracked across
// commits and line numbers move, so what one reports is not reliably about the reference it holds
// now, and the row carries only what was found in the version's own code.
func TestTheAdvisoriesTravelVerbatimAndACallersFindingIsNotAVersionsVerdict(t *testing.T) {
	f := releasesInUse(t)
	app, err := f.s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "findings/suite.qls", Repository: "octo/app", DBCommit: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddFindings([]store.Finding{{QueryRunID: run, RuleID: "actions/vulnerable-action",
		RepositoryID: app, FilePath: ".github/workflows/ci.yml", StartLine: 9, Severity: "error",
		ResultHash: "vuln", Fingerprint: "vuln"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB().Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
		 WHERE f.result_hash = 'vuln' AND n.node_key = 'octo/app-step'`); err != nil {
		t.Fatal(err)
	}
	affects(t, f.s, "GHSA-comp", "vendor/comp", "")

	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{})
	if v := versionNamed(t, page, "v1"); v.Findings != nil {
		t.Errorf("v1 carries %+v; nothing analyzed vendor/comp at c1, and octo/app's finding is not one of its",
			*v.Findings)
	}
	if len(page.Advisories) != 1 || page.Advisories[0].GHSAID != "GHSA-comp" ||
		len(page.Advisories[0].Ranges) != 1 || page.Advisories[0].Ranges[0].VulnerableRange != "< 99" {
		t.Errorf("advisories %+v, want GHSA-comp with its range verbatim", page.Advisories)
	}
}

// The counts in a row answer the same scope as the uses table: narrowing who is asked about
// narrows who is counted, a version nobody asked about uses is left out and said so, and a
// reference narrows which rows are listed while the total still says how many are in use.
func TestVersionCountsFollowTheScopeAndTheReferenceNarrowsTheList(t *testing.T) {
	f := releasesInUse(t)
	page := versionsOf(t, f.s, "vendor/comp", UsesQuery{Repositories: map[string]bool{"octo/app": true}})
	if got := len(page.Items); got != 1 || page.Unlisted != 3 {
		t.Errorf("octo/app alone lists %d versions and leaves out %d; want v1, and the 3 others said",
			got, page.Unlisted)
	}
	if v := versionNamed(t, page, "v1"); v.Direct != 1 {
		t.Errorf("v1 counts %d repositories, want octo/app", v.Direct)
	}
	one := versionsOf(t, f.s, "vendor/comp", UsesQuery{At: []string{"v2"}})
	if got := []string{}; len(one.Items) != 1 || one.Items[0].Reference != "v2" || one.Total != 4 {
		for _, v := range one.Items {
			got = append(got, v.Reference)
		}
		t.Errorf("at=v2 listed %v of %d in use, want v2 of 4", got, one.Total)
	}
	if styled := versionsOf(t, f.s, "vendor/comp", UsesQuery{RefStyle: "sha"}); len(styled.Items) != 0 {
		t.Errorf("ref=sha listed %d versions; every reference here is a tag", len(styled.Items))
	}
	if !slices.IsSortedFunc(page.Items, func(a, b Version) int {
		return (b.Direct + b.Transitive) - (a.Direct + a.Transitive)
	}) {
		t.Errorf("versions are not widest first: %+v", page.Items)
	}
}

// A repository that names a version and also reaches it through something else is one repository
// with a reference of its own to change: direct, and not counted again as transitive.
func TestARepositoryNamingAVersionAndReachingItThroughAnotherIsCountedOnceAsDirect(t *testing.T) {
	f := releasesInUse(t)
	app, err := f.s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	f.node(t, store.Node{Key: "octo/app-cache-step", Kind: "step", RepositoryID: app,
		ContainerKey: "octo/app/.github/workflows/ci.yml"})
	if err := f.w.AddEdge(store.Edge{Key: "octo/app:.github/workflows/ci.yml:12:9",
		SrcKey: "octo/app-cache-step", DstKey: "actions/cache", Kind: store.EdgeKindStepUsesAction,
		RefValue: "v3", RepositoryID: app, FilePath: ".github/workflows/ci.yml", StartLine: 12,
		StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)

	v := versionNamed(t, versionsOf(t, f.s, "actions/cache", UsesQuery{}), "v3")
	if v.Direct != 1 || v.Transitive != 1 {
		t.Errorf("actions/cache@v3: %d direct and %d transitive; want octo/app direct and only "+
			"octo/patched transitive", v.Direct, v.Transitive)
	}
}

// A reference says what the source wrote; the commits locks select behind it say what runs. Both,
// side by side, and a repository whose lock chose another commit is still counted under the
// reference it wrote.
func TestAVersionSaysWhichCommitsLocksSelectBehindIt(t *testing.T) {
	s := lockedVendorFixture(t)
	v := versionNamed(t, versionsOf(t, s, "vendor/action", UsesQuery{}), "v1")
	if v.Direct != 2 || v.Commit != vendorLive {
		t.Errorf("v1: %d direct, resolving to %s; want both repositories and the tag's commit", v.Direct, v.Commit)
	}
	if len(v.LockedTo) != 1 || v.LockedTo[0] != (LockedCommit{Commit: vendorLocked, Repositories: 1}) {
		t.Errorf("v1 is locked to %+v, want %s by one repository", v.LockedTo, vendorLocked)
	}
	if empty := versionNamed(t, versionsOf(t, releasesInUse(t).s, "vendor/comp", UsesQuery{}), "v1"); empty.LockedTo == nil {
		t.Error("an unlocked version's locks are nil; an empty list is the contract")
	}
}
