package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func recordCheck(t *testing.T, s *Store, run CheckRun) {
	t.Helper()
	if _, err := s.RecordCheckRun(run); err != nil {
		t.Fatalf("RecordCheckRun: %v", err)
	}
}

// A disagreement is one question however often it is asked, and its first build is when it started.
func TestADisagreementSeenByTwoBuildsIsOneQueueEntryFirstSeenAtTheFirst(t *testing.T) {
	s := newTestStore(t)
	seen := Disagreement{Subject: "actions/cache", Reading: "abom_node_reach", Class: "derivation",
		Claim: "octo/app|1", Independent: "octo/app|2"}
	for i, build := range []string{"v1 aaa reach-inputs/3", "v1 aaa reach-inputs/3",
		"v2 bbb reach-inputs/3", "v2 bbb reach-inputs/3"} {
		recordCheck(t, s, CheckRun{Check: 7, Build: build,
			StartedAt: []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04"}[i],
			Parts: []CheckPart{{Part: "abom_node_reach", Compared: 3,
				Disagreements: []Disagreement{seen}}}})
	}

	page, err := s.OpenDisagreements(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("the queue holds %d entries (%d listed), want the one disagreement every run saw",
			page.Total, len(page.Items))
	}
	got := page.Items[0]
	if got.FirstBuild != "v1 aaa reach-inputs/3" || got.FirstSeenAt != "2026-10-01" {
		t.Errorf("first seen at %q on %q, want the first build's first run", got.FirstBuild,
			got.FirstSeenAt)
	}
	if got.LastBuild != "v2 bbb reach-inputs/3" || got.LastSeenAt != "2026-10-04" {
		t.Errorf("last seen at %q on %q, want the second build's last run", got.LastBuild,
			got.LastSeenAt)
	}
	if got.Check != 7 || got.Subject != "actions/cache" || got.Reading != "abom_node_reach" ||
		got.Class != "derivation" || got.Claim != "octo/app|1" || got.Independent != "octo/app|2" {
		t.Errorf("the entry does not reproduce what was seen: %+v", got)
	}

	trend, err := s.CheckTrend()
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 2 {
		t.Fatalf("the trend has %d points, want one per build: %+v", len(trend), trend)
	}
	for i, want := range []struct {
		build         string
		runs, started int
	}{{"v1 aaa reach-inputs/3", 2, 1}, {"v2 bbb reach-inputs/3", 2, 0}} {
		p := trend[i]
		if p.Build != want.build || p.Runs != want.runs || p.Started != want.started {
			t.Errorf("trend point %d = %+v, want build %q with %d runs starting %d", i, p,
				want.build, want.runs, want.started)
		}
		if p.Disagreed == nil || *p.Disagreed != 1 {
			t.Errorf("trend point %d reports %v disagreements, want the 1 its last run saw", i,
				p.Disagreed)
		}
	}
}

// A check that read nothing found no agreement: zero disagreements from it would read as health.
func TestAnUnreadCheckIsSkippedWithItsReasonNotZeroDisagreements(t *testing.T) {
	s := newTestStore(t)
	seen := Disagreement{Subject: "actions/cache", Reading: "abom_node_reach", Class: "derivation"}
	recordCheck(t, s, CheckRun{Check: 7, Build: "b", StartedAt: "2026-10-01",
		Parts: []CheckPart{{Part: "abom_node_reach", Compared: 2,
			Disagreements: []Disagreement{seen}}}})
	refused := "Derive refused: stored reach carries no mark of what it was derived from"
	recordCheck(t, s, CheckRun{Check: 7, Build: "b", StartedAt: "2026-10-02",
		Parts: []CheckPart{{Part: "abom_node_reach",
			Skipped: []CheckSkip{{Reason: refused, Count: 1}}}}})

	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	run, ok := latest[7]
	if !ok || len(run.Parts) != 1 {
		t.Fatalf("the latest run of check 7 is %+v, want the skipped one with its part", run)
	}
	part := run.Parts[0]
	if part.Disagreed != nil {
		t.Errorf("a part that compared nothing reports %d disagreements; unread is not agreement",
			*part.Disagreed)
	}
	if len(part.Skipped) != 1 || part.Skipped[0].Reason != refused || part.Skipped[0].Count != 1 {
		t.Errorf("the part reports skipped %+v, want the refusal and its reason", part.Skipped)
	}
	trend, err := s.CheckTrend()
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 1 || trend[0].Disagreed != nil || trend[0].Skipped != 1 {
		t.Errorf("the trend reports %+v, want the build's last run as unread with one skip", trend)
	}
	if page, err := s.OpenDisagreements(0, 50); err != nil || page.Total != 1 {
		t.Errorf("after an unread run the queue holds %+v (%v), want the earlier disagreement open",
			page, err)
	}

	recordCheck(t, s, CheckRun{Check: 7, Build: "b", StartedAt: "2026-10-03",
		Parts: []CheckPart{{Part: "abom_node_reach", Compared: 2}}})
	if page, err := s.OpenDisagreements(0, 50); err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("after a run that compared and agreed the queue holds %+v (%v), want it closed",
			page, err)
	}
}

func openCount(t *testing.T, s *Store) int {
	t.Helper()
	page, err := s.OpenDisagreements(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}

// A sampled run reads only some subjects, so one it did not read again is unknown rather than
// resolved, and one it could not read is unread: only a later reading that agrees closes it.
func TestASampledDisagreementClosesOnlyWhenALaterRunReadsItAndAgrees(t *testing.T) {
	s := newTestStore(t)
	population, one := 40, 1
	stale := Disagreement{Subject: "actions/checkout@v4", Reading: "3 where the reference points",
		Class: "unnoticed", Claim: "ABOM stands at c1", Independent: "it points at c2 now"}
	random := "3c external references sampled at random"
	sampled := func(at string, p CheckPart) {
		recordCheck(t, s, CheckRun{Check: 3, Build: "b", StartedAt: at, Parts: []CheckPart{p}})
	}

	sampled("2026-10-01", CheckPart{Part: random, Population: &population, Compared: 10,
		Disagreements: []Disagreement{stale}})
	if got := openCount(t, s); got != 1 {
		t.Fatalf("the queue holds %d after the run that saw it, want 1", got)
	}
	sampled("2026-10-02", CheckPart{Part: random, Population: &population, Compared: 10})
	if got := openCount(t, s); got != 1 {
		t.Errorf("a run that read ten other references left %d open, want the one it never read again", got)
	}
	sampled("2026-10-03", CheckPart{Part: "3a open disagreements read again", Population: &one,
		Skipped: []CheckSkip{{Reason: "unread: GitHub did not say where it points", Count: 1}}})
	if got := openCount(t, s); got != 1 {
		t.Errorf("a run that could not read it left %d open; a skip closes nothing", got)
	}
	sampled("2026-10-04", CheckPart{Part: "3a open disagreements read again", Population: &one,
		Compared: 1, Agreed: []CheckSubject{{Subject: stale.Subject, Reading: stale.Reading}}})
	if got := openCount(t, s); got != 0 {
		t.Errorf("a run that read it again and agreed left %d open, want it closed", got)
	}
	sampled("2026-10-05", CheckPart{Part: random, Population: &population, Compared: 10,
		Disagreements: []Disagreement{stale}})
	if got := openCount(t, s); got != 1 {
		t.Errorf("seen again after it closed, the queue holds %d, want it open again", got)
	}
}

// A sampled part says what it was drawn from and what its sample bounds; an exhaustive part read
// everything it could, so it states neither.
func TestASampledPartReportsItsPopulationAndBoundAndAnExhaustiveOneNeither(t *testing.T) {
	s := newTestStore(t)
	population, bound := 1200, 0.0099
	recordCheck(t, s, CheckRun{Check: 3, Build: "b", StartedAt: "2026-10-01", Parts: []CheckPart{
		{Part: "3c external references sampled at random", Population: &population, Bound: &bound,
			Compared: 300}}})
	recordCheck(t, s, CheckRun{Check: 9, Build: "b", StartedAt: "2026-10-01", Parts: []CheckPart{
		{Part: "9a the Dashboard's dependency count is the Actions unit's total", Compared: 1}}})

	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	if p := latest[3].Parts[0]; p.Population == nil || *p.Population != population || p.Bound == nil ||
		*p.Bound != bound {
		t.Errorf("the sampled part reads back as %+v, want drawn from %d with a bound of %v", p, population, bound)
	}
	if p := latest[9].Parts[0]; p.Population != nil || p.Bound != nil {
		t.Errorf("the exhaustive part reads back as %+v, want no population and no bound", p)
	}
}

// The checker migrates only its own tables on the database it reads, so a column added to them has
// to reach tables an older build created there without the migration a serving process runs.
func TestSQLiteMigrateChecksAddsNewColumnsToCheckTablesAnOlderBuildCreated(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.DB().Exec(`
		CREATE TABLE abom_check_run (row_id INTEGER PRIMARY KEY, check_no INTEGER NOT NULL,
		  build TEXT NOT NULL, started_at TEXT NOT NULL, wall_ms INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE abom_check_part (run_row_id INTEGER NOT NULL REFERENCES abom_check_run(row_id),
		  check_no INTEGER NOT NULL, part TEXT NOT NULL, compared INTEGER NOT NULL, disagreed INTEGER,
		  PRIMARY KEY (run_row_id, part));
		CREATE TABLE abom_check_skip (run_row_id INTEGER NOT NULL REFERENCES abom_check_run(row_id),
		  part TEXT NOT NULL, reason TEXT NOT NULL, count INTEGER NOT NULL,
		  PRIMARY KEY (run_row_id, part, reason)) WITHOUT ROWID;
		CREATE TABLE abom_check_disagreement (row_id INTEGER PRIMARY KEY, check_no INTEGER NOT NULL,
		  part TEXT NOT NULL, subject TEXT NOT NULL, reading TEXT NOT NULL, class TEXT NOT NULL,
		  claim TEXT NOT NULL DEFAULT '', independent TEXT NOT NULL DEFAULT '', first_build TEXT NOT NULL,
		  first_seen_at TEXT NOT NULL, first_run_row_id INTEGER NOT NULL, last_build TEXT NOT NULL,
		  last_seen_at TEXT NOT NULL, last_run_row_id INTEGER NOT NULL,
		  UNIQUE (check_no, subject, reading));`); err != nil {
		t.Fatal(err)
	}

	if err := s.MigrateChecks(); err != nil {
		t.Fatalf("MigrateChecks: %v", err)
	}
	population := 3
	recordCheck(t, s, CheckRun{Check: 4, Build: "b", StartedAt: "2026-10-01", Parts: []CheckPart{
		{Part: "4c owned repositories sampled at random", Population: &population, Compared: 3,
			Disagreements: []Disagreement{{Subject: "octo/app", Reading: "4 default branch head",
				Class: "unnoticed"}}}}})
	if got := openCount(t, s); got != 1 {
		t.Errorf("the queue holds %d after a sampled run on migrated tables, want its one disagreement", got)
	}
}

// A sampled check reads its open disagreements again before drawing anything new, so it asks for
// exactly the open ones that are its own.
func TestOpenSubjectsAreTheOpenDisagreementsOfOneCheckAlone(t *testing.T) {
	s := newTestStore(t)
	population := 10
	file := func(check int, at string, p CheckPart) {
		recordCheck(t, s, CheckRun{Check: check, Build: "b", StartedAt: at, Parts: []CheckPart{p}})
	}
	file(3, "2026-10-01", CheckPart{Part: "3c", Population: &population, Compared: 5,
		Disagreements: []Disagreement{
			{Subject: "vendor/act@v1", Reading: "3 where the reference points", Class: "unnoticed"},
			{Subject: "vendor/tool@v2", Reading: "3 where the reference points", Class: "staleness"}}})
	file(3, "2026-10-02", CheckPart{Part: "3a", Population: &population, Compared: 1,
		Agreed: []CheckSubject{{Subject: "vendor/tool@v2", Reading: "3 where the reference points"}}})
	file(4, "2026-10-02", CheckPart{Part: "4c", Population: &population, Compared: 1,
		Disagreements: []Disagreement{{Subject: "octo/app", Reading: "4 default branch head",
			Class: "unnoticed"}}})

	got, err := s.OpenSubjects(3)
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckSubject{{Subject: "vendor/act@v1", Reading: "3 where the reference points"}}
	if !slices.Equal(got, want) {
		t.Errorf("check 3's open subjects are %+v, want %+v: the settled one and check 4's are not its own",
			got, want)
	}
}
