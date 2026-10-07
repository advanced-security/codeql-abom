package store

import (
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
