package store

import (
	"slices"
	"testing"
	"time"
)

func TestAPreviewIsApprovedOnceAndRunsAtMostOneBatch(t *testing.T) {
	s := setupFixture(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	id, err := s.RecordSetupPreview(SetupPreview{CreatedAt: at, CreatedBy: "interface", ViewName: "owned",
		Predicate: "scope=owned", Accounts: []string{"octo"}, Excluded: []string{"octo/held"},
		Rows: []SetupPreviewRow{
			{Repository: "octo/add", Class: SetupAdd, Evidence: "e", Languages: []string{"python"},
				Intended: `{"languages":["actions","python"]}`},
			{Repository: "octo/covered", Class: SetupCovered, Evidence: "e", Languages: []string{"actions"}},
		}})
	if err != nil {
		t.Fatalf("RecordSetupPreview: %v", err)
	}
	if _, err := s.StartSetupBatch(id, "interface", at); err == nil {
		t.Fatal("a batch started from a preview nobody approved")
	}
	if err := s.ApproveSetupPreview(id, "data-douser", at); err != nil {
		t.Fatalf("ApproveSetupPreview: %v", err)
	}
	if err := s.ApproveSetupPreview(id, "someone-else", at); err == nil {
		t.Error("a preview was approved twice")
	}
	batch, err := s.StartSetupBatch(id, "interface", at)
	if err != nil {
		t.Fatalf("StartSetupBatch: %v", err)
	}
	if _, err := s.StartSetupBatch(id, "interface", at); err == nil {
		t.Error("one preview started two batches")
	}
	p, ok, err := s.SetupPreviewOf(id)
	if err != nil || !ok || p.ApprovedBy != "data-douser" || p.BatchID != batch || p.Counts[SetupAdd] != 1 ||
		!slices.Equal(p.Excluded, []string{"octo/held"}) || len(p.Rows) != 2 {
		t.Errorf("preview read back as %+v (%v %v)", p, ok, err)
	}
	remaining, err := s.SetupBatchRemaining(batch)
	if err != nil || len(remaining) != 1 || remaining[0].Repository != "octo/add" {
		t.Errorf("a batch writes only its add and enable rows: %+v %v", remaining, err)
	}
}

func TestTheLedgerIsAppendedAndSaysWhatIsStillInFlight(t *testing.T) {
	s := setupFixture(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	id, err := s.RecordSetupPreview(SetupPreview{CreatedAt: at, Rows: []SetupPreviewRow{
		{Repository: "octo/add", Class: SetupAdd, Evidence: "e", Languages: []string{"python"}},
		{Repository: "octo/enable", Class: SetupEnable, Evidence: "e", Languages: []string{"actions"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveSetupPreview(id, "me", at); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartSetupBatch(id, "me", at)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []SetupWrite{
		{BatchID: batch, Repository: "octo/add", At: at, Step: SetupStepSent, Before: []string{"python"},
			Sent: `{"languages":["actions","python"]}`, Status: 202, RunID: 7},
		{BatchID: batch, Repository: "octo/enable", At: at, Step: SetupStepSent, Before: []string{}, Status: 200},
		{BatchID: batch, Repository: "octo/enable", At: at.Add(time.Minute), Step: SetupStepVerified,
			After: []string{"actions"}},
	} {
		if err := s.AppendSetupWrite(w); err != nil {
			t.Fatalf("AppendSetupWrite: %v", err)
		}
	}
	flying, err := s.SetupWritesInFlight()
	if err != nil || len(flying) != 1 || flying[0].Repository != "octo/add" || flying[0].RunID != 7 ||
		!slices.Equal(flying[0].Before, []string{"python"}) {
		t.Errorf("in flight: %+v %v, want octo/add alone", flying, err)
	}
	remaining, err := s.SetupBatchRemaining(batch)
	if err != nil || len(remaining) != 0 {
		t.Errorf("a repository with a write sent is not written again: %+v %v", remaining, err)
	}
	ledger, err := s.SetupLedger("", batch)
	if err != nil || len(ledger) != 3 {
		t.Errorf("ledger holds %d steps (%v), want all 3", len(ledger), err)
	}
}

// A repository's own answer about its configuration settles enforcement for it, so an account the
// App cannot read whole still lets its repositories be classified.
func TestARepositorysOwnConfigurationAnswerSettlesItsEnforcement(t *testing.T) {
	s := setupFixture(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if c := classOfStore(t, s, "solo/enable"); c.Class != SetupNotRead {
		t.Fatalf("solo/enable starts as %s", c.Class)
	}
	if err := s.RecordRepositoryConfiguration("solo/enable", nil, "", at); err != nil {
		t.Fatalf("RecordRepositoryConfiguration: %v", err)
	}
	if c := classOfStore(t, s, "solo/enable"); c.Class != SetupEnable {
		t.Errorf("with no configuration attached, solo/enable is %s, want enable", c.Class)
	}
	if err := s.RecordRepositoryConfiguration("solo/enable", &SecurityConfiguration{ID: 9, Name: "Locked",
		Enforcement: "enforced", DefaultSetup: "disabled"}, "enforced", at); err != nil {
		t.Fatalf("RecordRepositoryConfiguration: %v", err)
	}
	if c := classOfStore(t, s, "solo/enable"); c.Class != SetupBlocked || c.ConfigurationName != "Locked" {
		t.Errorf("under an enforced configuration that disables default setup: %s %q", c.Class, c.ConfigurationName)
	}
}

func classOfStore(t *testing.T, s *Store, repository string) SetupClass {
	t.Helper()
	classes, err := s.SetupClasses(repository)
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses(%s): %v %v", repository, classes, err)
	}
	return classes[0]
}

// An approver judges what would be written, so those rows lead and the rest follow by class. At
// twenty thousand repositories a page of them is what the browser is sent, so every row has to be
// on exactly one page.
func TestAPreviewPagesTheRowsItWouldWriteFirst(t *testing.T) {
	s := setupFixture(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	id, err := s.RecordSetupPreview(SetupPreview{CreatedAt: at, Rows: []SetupPreviewRow{
		{Repository: "octo/a-covered", Class: SetupCovered, Evidence: "e"},
		{Repository: "octo/b-not-read", Class: SetupNotRead, Evidence: "e"},
		{Repository: "octo/c-enable", Class: SetupEnable, Evidence: "e", Intended: `{"state":"configured"}`},
		{Repository: "octo/d-held", Class: "held", Evidence: "e"},
		{Repository: "octo/e-add", Class: SetupAdd, Evidence: "e", Intended: `{"languages":["actions","python"]}`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	for offset := 0; offset < 10; offset += 2 {
		page, err := s.SetupPreviewRowsPage(id, 2, offset)
		if err != nil {
			t.Fatalf("SetupPreviewRowsPage(offset %d): %v", offset, err)
		}
		if page.Total != 5 {
			t.Errorf("offset %d says the preview holds %d rows, want 5", offset, page.Total)
		}
		if len(page.Items) > 2 {
			t.Errorf("offset %d answered %d rows, more than the 2 asked for", offset, len(page.Items))
		}
		for _, row := range page.Items {
			seen = append(seen, row.Repository)
		}
	}
	want := []string{"octo/e-add", "octo/c-enable", "octo/d-held", "octo/b-not-read", "octo/a-covered"}
	if !slices.Equal(seen, want) {
		t.Errorf("paged through %v, want %v: what would be written first, every row once", seen, want)
	}
	if page, err := s.SetupPreviewRowsPage(id+1, 2, 0); err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("a preview that does not exist paged as %+v (%v), want nothing", page, err)
	}
}

// The panel shows the newest steps and says how many there are. Sending every step on every poll
// grows with the organization: a step or three for each of twenty thousand repositories.
func TestTheLedgerAnswersItsNewestStepsAndHowManyThereAre(t *testing.T) {
	s := setupFixture(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	id, err := s.RecordSetupPreview(SetupPreview{CreatedAt: at, Rows: []SetupPreviewRow{
		{Repository: "octo/add", Class: SetupAdd, Evidence: "e"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveSetupPreview(id, "me", at); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartSetupBatch(id, "me", at)
	if err != nil {
		t.Fatal(err)
	}
	for i, repository := range []string{"octo/add", "octo/enable", "octo/held"} {
		if err := s.AppendSetupWrite(SetupWrite{BatchID: batch, Repository: repository,
			At: at.Add(time.Duration(i) * time.Minute), Step: SetupStepSent}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.SetupLedgerNewest("", 0, 2)
	if err != nil {
		t.Fatalf("SetupLedgerNewest: %v", err)
	}
	got := []string{}
	for _, w := range page.Items {
		got = append(got, w.Repository)
	}
	if page.Total != 3 || !slices.Equal(got, []string{"octo/held", "octo/enable"}) {
		t.Errorf("newest 2 of the ledger: %v of %d, want [octo/held octo/enable] of 3", got, page.Total)
	}
}
