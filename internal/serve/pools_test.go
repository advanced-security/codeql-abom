package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

type poolBody struct {
	Pool      string `json:"pool"`
	Unit      string `json:"unit"`
	Work      string `json:"work"`
	Unmetered string `json:"unmetered"`
	PerHour   *int   `json:"perHour"`
	Burst     int    `json:"burst"`
	Spend     *struct {
		LastHour struct{ Requests, Charged int } `json:"lastHour"`
		Reported *struct {
			Remaining int `json:"remaining"`
		} `json:"reported"`
	} `json:"spend"`
	Estimate *struct {
		Text    string `json:"text"`
		Seconds int64  `json:"seconds"`
		Work    int    `json:"work"`
		Checked string `json:"checked"`
	} `json:"estimate"`
	NoEstimate string `json:"noEstimate"`
}

type poolsBody struct {
	Pools []poolBody `json:"pools"`
}

func (b poolsBody) find(t *testing.T, pool, unit string) poolBody {
	t.Helper()
	for _, p := range b.Pools {
		if p.Pool == pool && p.Unit == unit {
			return p
		}
	}
	t.Fatalf("no %s pool in %s", pool, unit)
	return poolBody{}
}

func pooledServer(t *testing.T) (*Server, *store.Store) {
	srv, s := newServer(t)
	srv.Pools = work.NewTokenPools()
	return srv, s
}

func putPool(srv *Server, body string, guarded bool) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPut, "/api/pools", strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if guarded {
		req.Header.Set(controlHeader, "1")
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// Every pool is listed, and a database that has never spent reports no spend rather than zero.
func TestTheMeterEndpointListsEveryPoolAndNoSpendWhereNothingWasSpent(t *testing.T) {
	srv, _ := pooledServer(t)

	body := decode[poolsBody](t, get(t, srv, "/api/pools"))

	if len(body.Pools) != len(work.PoolCatalog) {
		t.Fatalf("listed %d pools, want the catalog's %d", len(body.Pools), len(work.PoolCatalog))
	}
	for _, p := range body.Pools {
		if p.Spend != nil {
			t.Errorf("%s in %s never spent and reports %+v", p.Pool, p.Unit, p.Spend)
		}
		if p.PerHour != nil {
			t.Errorf("%s in %s has no rate set and reports %d", p.Pool, p.Unit, *p.PerHour)
		}
	}
	if got := body.find(t, work.PoolCopilot, work.UnitCopilot); got.Unmetered == "" {
		t.Error("a pool nothing in this build draws on has to say so")
	}
	// The checker draws on its own pools, which have no queue for an estimate to drain.
	for _, unit := range []string{work.UnitWorkerSeconds, work.UnitREST, work.UnitGraphQL} {
		got := body.find(t, work.PoolConsistency, unit)
		if got.Unmetered != "" {
			t.Errorf("the checker's %s pool says %q, and the checker draws on it", unit, got.Unmetered)
		}
		if got.Estimate != nil || got.NoEstimate == "" {
			t.Errorf("the checker's %s pool estimates %+v and says %q, want why it has no estimate",
				unit, got.Estimate, got.NoEstimate)
		}
	}
	// An event's follow-up reads GitHub through a client of its own, so this pool is metered.
	if got := body.find(t, work.PoolEvents, work.UnitREST); got.Unmetered != "" {
		t.Errorf("the event follow-up REST pool says %q, and the event processor draws on it", got.Unmetered)
	}
}

// The meter shows what a pool spent in the provider's unit and what the provider last reported.
func TestTheMeterReportsSpendAndWhatGitHubLastReported(t *testing.T) {
	srv, _ := pooledServer(t)
	p := srv.Pools.Pool(work.PoolEnumeration, work.UnitGraphQL)
	p.Spend(2, 7)
	p.Observe(work.Reading{Known: true, Remaining: 4993, Limit: 5000, ResetAt: time.Now().Add(time.Hour)})

	got := decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolEnumeration, work.UnitGraphQL)

	if got.Spend == nil || got.Spend.LastHour.Requests != 2 || got.Spend.LastHour.Charged != 7 {
		t.Fatalf("two queries charged seven points; got %+v", got.Spend)
	}
	if got.Spend.Reported == nil || got.Spend.Reported.Remaining != 4993 {
		t.Errorf("GitHub's last reported remaining is shown beside the spend; got %+v", got.Spend.Reported)
	}
}

// The checker runs as a process of its own and reads its rates from the store, so one set here is
// what paces it.
func TestARateOnTheCheckersPoolIsRecordedForTheChecker(t *testing.T) {
	srv, s := pooledServer(t)

	rec := putPool(srv, `{"pool":"consistency checker","unit":"GraphQL points","perHour":120}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("a rate on the checker's GraphQL pool answered %d: %s", rec.Code, rec.Body.String())
	}
	rates, err := s.PoolRates()
	if err != nil || len(rates) != 1 || rates[0].Pool != work.PoolConsistency ||
		rates[0].Unit != work.UnitGraphQL || rates[0].PerHour != 120 {
		t.Errorf("the rates recorded are %+v (%v), want the checker's 120 GraphQL points an hour", rates, err)
	}
}

// A rate set over the API is persisted and is the one the live pool enforces.
func TestSettingARatePersistsItAndTheLivePoolEnforcesIt(t *testing.T) {
	srv, s := pooledServer(t)

	rec := putPool(srv, `{"pool":"reference probing","unit":"git requests","perHour":600,"burst":4}`, true)
	got := decode[poolsBody](t, rec).find(t, work.PoolProbing, work.UnitGit)

	if got.PerHour == nil || *got.PerHour != 600 || got.Burst != 4 {
		t.Errorf("the answer shows the rate just set; got %v and %d", got.PerHour, got.Burst)
	}
	if rates, _ := s.PoolRates(); len(rates) != 1 || rates[0].PerHour != 600 {
		t.Errorf("the rate is persisted; got %+v", rates)
	}
	if perHour, burst := srv.Pools.Pool(work.PoolProbing, work.UnitGit).Rate(); perHour != 600 || burst != 4 {
		t.Errorf("the live pool enforces it; got %d with burst %d", perHour, burst)
	}

	rec = putPool(srv, `{"pool":"reference probing","unit":"git requests","perHour":null}`, true)
	if got := decode[poolsBody](t, rec).find(t, work.PoolProbing, work.UnitGit); got.PerHour != nil {
		t.Errorf("a cleared rate is none; got %d", *got.PerHour)
	}
	if perHour, _ := srv.Pools.Pool(work.PoolProbing, work.UnitGit).Rate(); perHour != 0 {
		t.Errorf("a cleared rate leaves the work alone; the pool still holds %d", perHour)
	}
}

// A rate is a write, guarded like every other, and refused where it could not mean anything.
func TestARateIsGuardedAndRefusedWhereItWouldLimitNothing(t *testing.T) {
	srv, s := pooledServer(t)

	for _, tc := range []struct {
		name    string
		body    string
		guarded bool
		want    int
	}{
		{"without the control header", `{"pool":"fetching","unit":"git requests","perHour":60}`, false, http.StatusForbidden},
		{"on a pool nothing draws on", `{"pool":"Copilot agents","unit":"Copilot credits","perHour":60}`, true, http.StatusBadRequest},
		{"on a pool that does not exist", `{"pool":"fetching","unit":"GraphQL points","perHour":60}`, true, http.StatusBadRequest},
		{"at zero, which would hold the work for ever", `{"pool":"fetching","unit":"git requests","perHour":0}`, true, http.StatusBadRequest},
		{"with a burst below one", `{"pool":"fetching","unit":"git requests","perHour":60,"burst":0}`, true, http.StatusBadRequest},
	} {
		if got := putPool(srv, tc.body, tc.guarded).Code; got != tc.want {
			t.Errorf("a rate %s answered %d, want %d", tc.name, got, tc.want)
		}
	}
	if rates, _ := s.PoolRates(); len(rates) != 0 {
		t.Errorf("nothing refused may be recorded; got %+v", rates)
	}
}

func seedReferences(t *testing.T, s *store.Store) {
	t.Helper()
	sha := strings.Repeat("a", 40)
	for _, row := range []struct {
		key, ref, status, resolved, analyzed string
	}{
		{"actions/checkout", "v4", "done", sha, "v4"},
		{"actions/cache", "", "done", sha, ""},
		{"actions/setup-go", sha, "done", sha, sha},
		{"actions/upload-artifact", "v4", "done", sha, sha},
		{"actions/download-artifact", "v4", "done", "", "v4"},
		{"actions/labeler", "v5", "pending", "", ""},
		{"actions/stale", "v9", "pending", "", ""},
	} {
		owner, repo, _ := strings.Cut(row.key, "/")
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref)
			VALUES (?, ?, ?, ?, 1, ?, '2026-10-01T00:00:00Z', NULLIF(?, ''), NULLIF(?, ''))`,
			row.key, row.ref, owner, repo, row.status, row.resolved, row.analyzed); err != nil {
			t.Fatal(err)
		}
	}
}

// The references the estimate counts are exactly the ones the sweep would ask about, or the time it
// derives describes some other population.
func TestTheProbingEstimateCountsWhatTheSweepWouldProbe(t *testing.T) {
	srv, s := pooledServer(t)
	seedReferences(t, s)
	srv.Pools.Pool(work.PoolProbing, work.UnitGit).Configure(3600, 1)

	due, err := (&frontier.Queue{Store: s}).DueForRecheck(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	got := decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolProbing, work.UnitGit)

	if got.Estimate == nil || got.Estimate.Work != len(due) || len(due) != 2 {
		t.Fatalf("the sweep would ask about %d references; the estimate counts %+v", len(due), got.Estimate)
	}
}

// Bounded to the owned closure, the sweep probes and the crawl fetches only inside it, and the
// estimates count the same populations.
func TestTheEstimatesCountOnlyWhatTheBoundedCrawlWouldDo(t *testing.T) {
	srv, s := pooledServer(t)
	seedReferences(t, s)
	srv.Pools.Pool(work.PoolProbing, work.UnitGit).Configure(3600, 1)
	srv.Pools.Pool(work.PoolFetching, work.UnitGit).Configure(3600, 1)
	closure := func() ([]string, error) { return []string{"actions/checkout", "actions/labeler"}, nil }
	srv.QueueClosure = closure

	due, err := (&frontier.Queue{Store: s, Closure: closure}).DueForRecheck(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	pools := decode[poolsBody](t, get(t, srv, "/api/pools"))
	probing := pools.find(t, work.PoolProbing, work.UnitGit)
	if probing.Estimate == nil || probing.Estimate.Work != len(due) || len(due) != 1 {
		t.Errorf("the bounded sweep would ask about %d references; the estimate counts %+v",
			len(due), probing.Estimate)
	}
	fetching := pools.find(t, work.PoolFetching, work.UnitGit)
	if fetching.Estimate == nil || fetching.Estimate.Work != 1 {
		t.Errorf("one queued reference is inside the closure; the estimate counts %+v", fetching.Estimate)
	}
}

// A freshness estimate follows from the rate and the work, says so, and says nothing has measured it.
func TestAFreshnessEstimateIsDerivedFromTheRateAndNeverPromised(t *testing.T) {
	srv, s := pooledServer(t)
	seedReferences(t, s)
	srv.RecheckFloor = 24 * time.Hour
	probing := srv.Pools.Pool(work.PoolProbing, work.UnitGit)

	got := decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolProbing, work.UnitGit)
	if got.Estimate != nil || !strings.Contains(got.NoEstimate, "No rate is set") {
		t.Errorf("with no rate set, no time follows from one; got %+v and %q", got.Estimate, got.NoEstimate)
	}

	probing.Configure(1, 1)
	got = decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolProbing, work.UnitGit)
	if got.Estimate == nil || got.Estimate.Seconds != int64((24*time.Hour).Seconds()) {
		t.Fatalf("two references at one an hour fall inside the 24-hour recheck floor; got %+v", got.Estimate)
	}
	if !strings.Contains(got.Estimate.Text, "about") || !strings.Contains(got.Estimate.Text, "estimate") {
		t.Errorf("the time is worded as an estimate from the rate; got %q", got.Estimate.Text)
	}
	if !strings.Contains(got.Estimate.Checked, "Nothing has measured") || !strings.Contains(got.Estimate.Checked, "check 10") {
		t.Errorf("an estimate nothing has measured says so and names what will; got %q", got.Estimate.Checked)
	}

	probing.Configure(60*60, 1)
	srv.RecheckFloor = 0
	got = decode[poolsBody](t, get(t, srv, "/api/pools")).find(t, work.PoolProbing, work.UnitGit)
	if got.Estimate == nil || got.Estimate.Seconds != 2 {
		t.Errorf("two references at one a second take about two seconds; got %+v", got.Estimate)
	}
}
