package enumerate

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
)

// setups records which repositories and accounts were read, which is the cost these tests are about.
type setups struct {
	mu           sync.Mutex
	repositories []string
	accounts     []string
	fail         map[string]error
	// flying and most count reads in progress at once.
	flying, most int
	// hold keeps each read in flight until this many have started, or a moment has passed.
	hold int
	all  chan struct{}
}

func (s *setups) read(_ context.Context, repository string) error {
	s.mu.Lock()
	s.repositories = append(s.repositories, repository)
	s.flying++
	s.most = max(s.most, s.flying)
	started := len(s.repositories)
	s.mu.Unlock()
	if s.hold > 0 {
		if started == s.hold {
			close(s.all)
		}
		select {
		case <-s.all:
		case <-time.After(2 * time.Second):
		}
	}
	s.mu.Lock()
	s.flying--
	s.mu.Unlock()
	return s.fail[repository]
}

func (s *setups) sorted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.repositories)
	slices.Sort(out)
	return out
}

func (s *setups) configurations(_ context.Context, account string) error {
	s.accounts = append(s.accounts, account)
	return s.fail[account]
}

func TestAListingRecordsWhereEachRepositoryKeepsItsActionsCode(t *testing.T) {
	s := newStore(t)
	yes, no := true, false
	listing := catalog.Listing{Route: catalog.RouteGraphQL, Full: true, Repositories: []catalog.Repository{
		{FullName: "has-ghas/one", DefaultBranch: "main", Fork: &no, Visibility: "PRIVATE",
			HasWorkflows: &yes, HasActionsDir: &no, HasRootAction: &yes}}}
	if _, err := Run(context.Background(), Options{Store: s, Read: byAccount(map[string]catalog.Listing{"has-ghas": listing})}); err != nil {
		t.Fatal(err)
	}
	f, ok, err := s.ListedFactsOf("has-ghas/one")
	if err != nil || !ok {
		t.Fatalf("ListedFactsOf: %v %v", ok, err)
	}
	if f.HasRootAction == nil || !*f.HasRootAction || f.Visibility != "private" || f.Fork == nil || *f.Fork {
		t.Errorf("listing facts recorded as %+v", f)
	}
}

func TestAFullListingReadsSetupForEveryOwnedRepositoryAndConfigurationsOnce(t *testing.T) {
	s := newStore(t)
	if _, err := s.InsertUpstreamRepository("someone/else"); err != nil {
		t.Fatal(err)
	}
	listing := catalog.Listing{Route: catalog.RouteGraphQL, Full: true,
		Repositories: onMain("has-ghas/one", "has-ghas/two")}
	read := &setups{fail: map[string]error{"has-ghas/two": errors.New("502 Bad Gateway")}}
	report, err := Run(context.Background(), Options{Store: s,
		Read:  byAccount(map[string]catalog.Listing{"has-ghas": listing}),
		Setup: read.read, Configurations: read.configurations})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(read.sorted(), []string{"has-ghas/one", "has-ghas/two"}) {
		t.Errorf("setup read for %v, want both owned repositories the listing named", read.repositories)
	}
	if !slices.Equal(read.accounts, []string{"has-ghas"}) {
		t.Errorf("configurations read for %v, want has-ghas once", read.accounts)
	}
	one := report.Accounts[0]
	if one.SetupRead != 1 || one.SetupUnread != 1 || one.SetupUnreadWhy == "" || !one.ConfigurationsRead {
		t.Errorf("report: %d read, %d unread (%q), configurations %v",
			one.SetupRead, one.SetupUnread, one.SetupUnreadWhy, one.ConfigurationsRead)
	}
}

// A sweep names what moved, and configurations are an account's whole answer: only a full
// listing reads them, or every sweep would pay for every configuration's repositories again.
func TestASweepReadsSetupForWhatMovedAndLeavesConfigurationsAlone(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", catalog.RouteGraphQL, 9, now, now); err != nil {
		t.Fatal(err)
	}
	read := &setups{}
	sweep := catalog.Listing{Route: catalog.RouteGraphQL, Repositories: onMain("has-ghas/moved")}
	_, err := Run(context.Background(), Options{Store: s, Read: byAccount(map[string]catalog.Listing{"has-ghas": sweep}),
		Setup: read.read, Configurations: read.configurations, now: func() time.Time { return now.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(read.sorted(), []string{"has-ghas/moved"}) || len(read.accounts) != 0 {
		t.Errorf("sweep read setup for %v and configurations for %v; want has-ghas/moved and none",
			read.repositories, read.accounts)
	}
}

// A configuration read that fails is reported against the account and stops nothing else.
func TestAConfigurationReadThatFailsIsReportedNotFatal(t *testing.T) {
	s := newStore(t)
	listing := catalog.Listing{Route: catalog.RouteGraphQL, Full: true, Repositories: onMain("has-ghas/one")}
	read := &setups{fail: map[string]error{"has-ghas": errors.New("403 Forbidden")}}
	report, err := Run(context.Background(), Options{Store: s,
		Read:  byAccount(map[string]catalog.Listing{"has-ghas": listing}),
		Setup: read.read, Configurations: read.configurations})
	if err != nil {
		t.Fatal(err)
	}
	if one := report.Accounts[0]; one.ConfigurationsRead || one.ConfigurationsUnread != "403 Forbidden" || one.SetupRead != 1 {
		t.Errorf("report after a refused configuration read: %+v", one)
	}
}

// A full listing of a large organization would hold the crawl for hours one read at a time, so
// reads overlap; every failure is still counted, and the first is named in the listing's order.
func TestSetupReadsOverlapAndEveryFailureIsCounted(t *testing.T) {
	s := newStore(t)
	listing := catalog.Listing{Route: catalog.RouteGraphQL, Full: true,
		Repositories: onMain("has-ghas/a", "has-ghas/b", "has-ghas/c")}
	read := &setups{hold: 3, all: make(chan struct{}), fail: map[string]error{
		"has-ghas/b": errors.New("502 Bad Gateway"), "has-ghas/c": errors.New("503 Service Unavailable")}}
	report, err := Run(context.Background(), Options{Store: s,
		Read: byAccount(map[string]catalog.Listing{"has-ghas": listing}), Setup: read.read})
	if err != nil {
		t.Fatal(err)
	}
	if read.most < 2 {
		t.Errorf("at most %d reads were in flight at once, want them to overlap", read.most)
	}
	one := report.Accounts[0]
	if one.SetupRead != 1 || one.SetupUnread != 2 || one.SetupUnreadWhy != "has-ghas/b: 502 Bad Gateway" {
		t.Errorf("report: %d read, %d unread, first %q", one.SetupRead, one.SetupUnread, one.SetupUnreadWhy)
	}
}
