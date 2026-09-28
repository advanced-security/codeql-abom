package enumerate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/graph"
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

	owned, err := s.OwnedRepositories()
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 0 {
		t.Errorf("active owned roots = %v, want the absent repository excluded", owned)
	}
	origin, err := s.RepositoryOrigin("has-ghas/vanished")
	if err != nil || origin != store.OriginSeed {
		t.Errorf("stored provenance = %q, error = %v; want the seed evidence retained", origin, err)
	}
}

// A full listing that no longer names a repository removes nothing. What the repository alone
// reached is what no longer belongs, and the prune in Settings says what it would remove before
// anybody accepts it: removing it here, on a timer, would take out real evidence whenever a
// listing under-reports without failing.
func TestAFullListingRemovesNothingAndLeavesWhatItLostToThePrune(t *testing.T) {
	s := newStore(t)
	repositories := map[string]int64{}
	for _, name := range []string{"has-ghas/app", "has-ghas/shared", "has-ghas/vanished", "stranger/unrelated"} {
		id, err := s.InsertOwnedRepository(name)
		if err != nil {
			t.Fatal(err)
		}
		repositories[name] = id
	}
	writer := store.NewWriter(s, 0)
	for name, id := range repositories {
		if err := writer.AddNode(store.Node{Key: name + "/.github/workflows/ci.yml", Kind: "workflow",
			RepositoryID: id, Repo: name, Path: ".github/workflows/ci.yml"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []store.Node{
		{Key: "has-ghas/shared", Kind: "action", Owner: "has-ghas", Repo: "shared", IsExternal: true},
		{Key: "upstream/only-vanished-uses", Kind: "action", Owner: "upstream", Repo: "only-vanished-uses", IsExternal: true},
	} {
		if err := writer.AddNode(target); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []store.Edge{
		{Key: "app-shared", SrcKey: "has-ghas/app/.github/workflows/ci.yml", DstKey: "has-ghas/shared",
			Kind: "step-uses-action", RepositoryID: repositories["has-ghas/app"], FilePath: ".github/workflows/ci.yml", StartLine: 4},
		{Key: "vanished-only", SrcKey: "has-ghas/vanished/.github/workflows/ci.yml", DstKey: "upstream/only-vanished-uses",
			Kind: "step-uses-action", RepositoryID: repositories["has-ghas/vanished"], FilePath: ".github/workflows/ci.yml", StartLine: 4},
	} {
		if err := writer.AddEdge(edge); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	live := func() int {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node_live`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	explained := func() store.PruneReport {
		keep, _, err := (&graph.Builder{Store: s}).KeepList()
		if err != nil {
			t.Fatal(err)
		}
		report, err := s.ExplainOutsideClosure(keep)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	before, beforeLive := explained(), live()

	listed := &reader{listings: []catalog.Listing{{Full: true, Route: catalog.RouteGraphQL,
		Repositories: []catalog.Repository{{FullName: "has-ghas/app"}, {FullName: "has-ghas/shared"}}}}}
	report, err := Run(context.Background(), Options{Store: s, Read: listed.read})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Accounts[0]; got.Missing != 1 || !got.MissingKnown {
		t.Fatalf("the listing reported missing=%d known=%v, want 1 known", got.Missing, got.MissingKnown)
	}
	if got := live(); got != beforeLive {
		t.Errorf("the listing left %d live nodes of %d; an enumeration removes nothing", got, beforeLive)
	}
	after := explained()
	if after.Nodes-before.Nodes != 2 || after.Edges-before.Edges != 1 {
		t.Errorf("the prune would remove %+v after the listing and %+v before, want exactly the missing "+
			"repository's workflow and the dependency only it used on top", after, before)
	}
}

func TestFailedReadsAndSweepsPreserveActiveRepositories(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := newStore(t)
		if _, err := s.InsertOwnedRepository("has-ghas/kept"); err != nil {
			t.Fatal(err)
		}
		read := func(context.Context, string, catalog.Marks) (catalog.Listing, error) {
			if fail {
				return catalog.Listing{}, errors.New("access refused")
			}
			return catalog.Listing{Full: false}, nil
		}
		if _, err := Run(context.Background(), Options{Store: s, Read: read}); err != nil {
			t.Fatal(err)
		}
		owned, err := s.OwnedRepositories()
		if err != nil || len(owned) != 1 {
			t.Fatalf("failed=%v: active roots=%v, error=%v", fail, owned, err)
		}
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

// A sweep is two ordered walks, so an account whose whole listing fits in one page is cheaper read
// whole. That read also answers what is gone, which a sweep cannot.
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
