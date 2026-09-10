package enumerate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	return s
}

// reader records what it was asked for, which is the thing most of these tests are about: the cost
// of a read is decided by the marks handed to it.
type reader struct {
	asked    []catalog.Marks
	listings []catalog.Listing
}

func (r *reader) read(_ context.Context, _ string, since catalog.Marks) (catalog.Listing, error) {
	r.asked = append(r.asked, since)
	if len(r.listings) == 0 {
		return catalog.Listing{Route: catalog.RouteGraphQL, Full: since.Empty()}, nil
	}
	next := r.listings[0]
	if len(r.listings) > 1 {
		r.listings = r.listings[1:]
	}
	return next, nil
}

func repo(name string, pushed time.Time) catalog.Repository {
	return catalog.Repository{FullName: name, PushedAt: pushed, CreatedAt: pushed}
}

// An account with no listing behind it has an unknown shape. Reading it as "no marks, so nothing is
// newer than the marks" would call one page of a sorted order a complete read of the account.
func TestAnAccountNobodyHasReadIsListedInFull(t *testing.T) {
	s := newStore(t)
	pushed := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	r := &reader{listings: []catalog.Listing{{
		Repositories: []catalog.Repository{repo("has-ghas/one", pushed)},
		Route:        catalog.RouteGraphQL,
		Full:         true,
		Marks:        catalog.Marks{HighCreated: pushed, HighPushed: pushed},
	}}}

	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}

	if len(r.asked) != 1 || !r.asked[0].Empty() {
		t.Errorf("asked with %+v, want no marks so the read is a full listing", r.asked)
	}
	if report.Added != 1 {
		t.Errorf("Added = %d, want the one repository the listing found", report.Added)
	}
	marks, known, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if !known || !marks.HighPushed.Equal(pushed) || marks.ListedAt.IsZero() {
		t.Errorf("state %+v (known=%v), want the full listing and its marks recorded", marks, known)
	}
}

// The whole point of the marks is that the next read is bounded by what moved rather than by how
// big the account is. A second run that asks with no marks has spent a second full listing.
func TestASecondRunSweepsFromTheMarksTheFirstLeft(t *testing.T) {
	s := newStore(t)
	pushed := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 3, pushed, pushed); err != nil {
		t.Fatal(err)
	}
	r := &reader{}

	if _, err := Run(context.Background(), Options{Store: s, Read: r.read}); err != nil {
		t.Fatal(err)
	}

	if len(r.asked) != 1 {
		t.Fatalf("made %d reads, want 1", len(r.asked))
	}
	if !r.asked[0].HighPushed.Equal(pushed) {
		t.Errorf("swept from %v, want the mark the full listing left at %v", r.asked[0].HighPushed, pushed)
	}
}

// A repository that no longer exists appears in no listing, so a sorted prefix can never mention
// it. Reporting zero missing after a sweep would say "nothing is gone" when nobody looked.
func TestOnlyAFullListingCanSayARepositoryIsGone(t *testing.T) {
	s := newStore(t)
	if _, err := s.InsertOwnedRepository("has-ghas/vanished"); err != nil {
		t.Fatal(err)
	}
	pushed := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 3, pushed, pushed); err != nil {
		t.Fatal(err)
	}

	swept := &reader{listings: []catalog.Listing{{Route: catalog.RouteGraphQL, Full: false}}}
	report, err := Run(context.Background(), Options{Store: s, Read: swept.read})
	if err != nil {
		t.Fatal(err)
	}
	if report.Accounts[0].MissingKnown {
		t.Error("a sweep read a prefix; it cannot know what is absent from the whole account")
	}

	listed := &reader{listings: []catalog.Listing{{Route: catalog.RouteGraphQL, Full: true}}}
	report, err = Run(context.Background(), Options{
		Store: s, Read: listed.read, FullListingEvery: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accounts[0].MissingKnown || report.Accounts[0].Missing != 1 {
		t.Errorf("full listing reported missing=%d known=%v, want 1 and known",
			report.Accounts[0].Missing, report.Accounts[0].MissingKnown)
	}

	// Naming what is absent is not the same as removing it, and which of those should happen is
	// an open question. Deciding it here, silently, would destroy coverage nobody agreed to lose.
	owned, err := s.OwnedRepositories()
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 {
		t.Errorf("corpus holds %v, want the absent repository kept until somebody decides", owned)
	}
}

// Sweeping forever is how a corpus keeps dead repositories while looking more current than one that
// reads everything. The full listing has to come round again.
func TestTheFullListingComesRoundAgainOnItsPeriod(t *testing.T) {
	s := newStore(t)
	pushed := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 3, pushed, pushed); err != nil {
		t.Fatal(err)
	}

	fresh := &reader{}
	if _, err := Run(context.Background(), Options{
		Store: s, Read: fresh.read, FullListingEvery: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if fresh.asked[0].Empty() {
		t.Error("the listing ran a moment ago; this read should have been a sweep")
	}

	due := &reader{}
	if _, err := Run(context.Background(), Options{
		Store: s, Read: due.read, FullListingEvery: time.Nanosecond}); err != nil {
		t.Fatal(err)
	}
	if !due.asked[0].Empty() {
		t.Errorf("asked with %+v, want no marks so the period forces a full listing", due.asked[0])
	}
}

// A sweep is two ordered walks, so it costs two reads at minimum. An account whose whole listing
// fit in one page is cheaper read whole — and that read also answers what is gone, which a sweep
// cannot. Measured on a 27 repository organization: one point for the listing, two for the sweep.
func TestAnAccountThatFitsOnOnePageIsReadWholeRatherThanSwept(t *testing.T) {
	s := newStore(t)
	pushed := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 1, pushed, pushed); err != nil {
		t.Fatal(err)
	}

	small := &reader{}
	if _, err := Run(context.Background(), Options{Store: s, Read: small.read}); err != nil {
		t.Fatal(err)
	}
	if !small.asked[0].Empty() {
		t.Errorf("asked with %+v, want a full listing: sweeping one page costs more than reading it",
			small.asked[0])
	}

	// Three pages is where the saving begins, because the sweep's two walks are then fewer reads
	// than the listing they replace.
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 3, pushed, pushed); err != nil {
		t.Fatal(err)
	}
	large := &reader{}
	if _, err := Run(context.Background(), Options{Store: s, Read: large.read}); err != nil {
		t.Fatal(err)
	}
	if large.asked[0].Empty() {
		t.Error("a three page account is worth sweeping, and was read in full instead")
	}
}

// Which route answered is a fact about the read, not an inference from the build. Two routes that
// disagree are only diagnosable if each figure can be attributed to the one that produced it.
func TestTheRouteThatAnsweredIsCarriedToTheReport(t *testing.T) {
	s := newStore(t)
	r := &reader{listings: []catalog.Listing{{
		Route: catalog.RouteREST, Full: true, Fallback: "no graphql here"}}}

	report, err := Run(context.Background(), Options{Store: s, Read: r.read})
	if err != nil {
		t.Fatal(err)
	}
	if report.Accounts[0].Route != catalog.RouteREST {
		t.Errorf("route %q, want %q", report.Accounts[0].Route, catalog.RouteREST)
	}
	if report.Accounts[0].Fallback == "" {
		t.Error("why the preferred route did not answer has to reach the reader")
	}

	marks, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if marks.Route != catalog.RouteREST {
		t.Errorf("stored route %q, want %q", marks.Route, catalog.RouteREST)
	}
}
