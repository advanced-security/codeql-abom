package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/checks"
	"github.com/advanced-security/codeql-abom/internal/poll"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// PoolsReport is every pool, its rate, what it spent and the freshness its rate buys.
type PoolsReport struct {
	Pools []PoolState `json:"pools"`
	// Since is where the last hour each meter reports starts.
	Since time.Time `json:"since"`
}

// PoolState is one pool.
type PoolState struct {
	work.PoolKind
	// PerHour is nil where no rate is set, which leaves the work alone.
	PerHour *int `json:"perHour"`
	Burst   int  `json:"burst"`
	// Spend is nil for a pool that has never spent, which is not a spend of zero.
	Spend      *store.PoolMeter `json:"spend"`
	Estimate   *Estimate        `json:"estimate"`
	NoEstimate string           `json:"noEstimate,omitempty"`
}

// Estimate is how long a pool's queued work takes at its rate: derived, never promised.
type Estimate struct {
	Text    string `json:"text"`
	Seconds int64  `json:"seconds"`
	// Work is the count of queued work the estimate is derived from.
	Work int `json:"work"`
	// Checked says what has measured the estimate, or what will.
	Checked string `json:"checked"`
}

func (s *Server) poolsReport(w http.ResponseWriter, _ *http.Request) {
	out, err := s.readPools()
	writeJSON(w, out, err)
}

func (s *Server) readPools() (PoolsReport, error) {
	s.FlushPools()
	since := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	out := PoolsReport{Pools: make([]PoolState, 0, len(work.PoolCatalog)), Since: since}
	meters, err := s.Store.PoolMeters(since)
	if err != nil {
		return out, err
	}
	rates, err := s.poolRates()
	if err != nil {
		return out, err
	}
	estimates, err := s.estimator()
	if err != nil {
		return out, err
	}
	for _, k := range work.PoolCatalog {
		p := PoolState{PoolKind: k, Burst: 1}
		if r, ok := rates[k.Pool+"\x00"+k.Unit]; ok {
			p.PerHour, p.Burst = &r.PerHour, r.Burst
		}
		for i := range meters {
			if meters[i].Pool == k.Pool && meters[i].Unit == k.Unit {
				p.Spend = &meters[i]
			}
		}
		if k.Unmetered == "" {
			p.Estimate, p.NoEstimate = estimates.estimate(k, p.PerHour)
		}
		out.Pools = append(out.Pools, p)
	}
	return out, nil
}

// poolRates reads what the live pools enforce, or what the database records where nothing does.
func (s *Server) poolRates() (map[string]store.PoolRate, error) {
	out := map[string]store.PoolRate{}
	if s.Pools == nil {
		rates, err := s.Store.PoolRates()
		for _, r := range rates {
			out[r.Pool+"\x00"+r.Unit] = r
		}
		return out, err
	}
	for _, p := range s.Pools.All() {
		if perHour, burst := p.Rate(); perHour > 0 {
			out[p.Kind.Pool+"\x00"+p.Kind.Unit] = store.PoolRate{PerHour: perHour, Burst: burst}
		}
	}
	return out, nil
}

// FlushPools records what the pools spent since the last flush. Spend that could not be written is
// put back, so a failed write delays it rather than losing it.
func (s *Server) FlushPools() {
	if s.Pools == nil {
		return
	}
	if err := RecordPoolSpend(s.Store, s.Pools); err != nil && s.Log != nil {
		fmt.Fprintf(s.Log, "%s serve: recording what the pools spent: %v\n",
			time.Now().UTC().Format(time.RFC3339), err)
	}
}

// RecordPoolSpend writes what a process's pools spent since they were last drained, where every
// meter is read, putting back what could not be written.
func RecordPoolSpend(s store.Backend, pools *work.TokenPools) error {
	drained := pools.Drain()
	rows := make([]store.PoolSpend, 0, len(drained))
	for _, d := range drained {
		rows = append(rows, store.PoolSpend{
			Pool: d.Pool, Unit: d.Unit, Bucket: d.Bucket, Requests: d.Requests, Charged: d.Charged,
			HasReading: d.Reading.Known, Remaining: d.Reading.Remaining, Limit: d.Reading.Limit,
			ResetAt: d.Reading.ResetAt,
		})
	}
	if err := s.AddPoolSpend(rows); err != nil {
		pools.Restore(drained)
		return err
	}
	return nil
}

// MeterPools flushes the pools on a period until the context ends, then once more.
func (s *Server) MeterPools(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.FlushPools()
			return
		case <-tick.C:
			s.FlushPools()
		}
	}
}

type poolRateRequest struct {
	Pool    string `json:"pool"`
	Unit    string `json:"unit"`
	PerHour *int   `json:"perHour"`
	Burst   *int   `json:"burst"`
}

// setPoolRate sets or clears one pool's rate. Guarded like every other write, and refused for a
// pool nothing in this build draws on, where a rate would limit nothing while reading as a limit.
func (s *Server) setPoolRate(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var req poolRateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "expected {pool, unit, perHour, burst}")
		return
	}
	kind, ok := poolKind(req.Pool, req.Unit)
	switch {
	case !ok:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("there is no %s pool in %s", req.Pool, req.Unit))
		return
	case kind.Unmetered != "":
		writeError(w, http.StatusBadRequest, kind.Unmetered+" A rate here would limit nothing.")
		return
	}
	if req.PerHour == nil {
		if err := s.Store.ClearPoolRate(kind.Pool, kind.Unit); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.Pools.Pool(kind.Pool, kind.Unit).Configure(0, 1)
	} else {
		burst := 1
		if req.Burst != nil {
			burst = *req.Burst
		}
		if err := s.Store.SetPoolRate(store.PoolRate{
			Pool: kind.Pool, Unit: kind.Unit, PerHour: *req.PerHour, Burst: burst,
		}); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.Pools.Pool(kind.Pool, kind.Unit).Configure(*req.PerHour, burst)
	}
	out, err := s.readPools()
	writeJSON(w, out, err)
}

func poolKind(pool, unit string) (work.PoolKind, bool) {
	for _, k := range work.PoolCatalog {
		if k.Pool == pool && k.Unit == unit {
			return k, true
		}
	}
	return work.PoolKind{}, false
}

// estimator holds the queued work estimates are derived from, read once per report.
type estimator struct {
	queued   store.FreshnessWork
	polling  store.PollWork
	packages int
	floor    time.Duration
}

func (s *Server) estimator() (estimator, error) {
	bound, err := s.queueBound()
	if err != nil {
		return estimator{}, err
	}
	queued, err := s.Store.ReadFreshnessWork(bound)
	if err != nil {
		return estimator{}, err
	}
	polling, err := s.Store.ReadPollWork(bound)
	if err != nil {
		return estimator{}, err
	}
	packages, err := s.Store.AdvisoryPackages()
	return estimator{queued: queued, polling: polling, packages: len(packages), floor: s.RecheckFloor}, err
}

// queueBound is the closure the crawl claims inside, as a store read takes it.
func (s *Server) queueBound() (store.QueueBound, error) {
	if s.QueueClosure == nil {
		return store.QueueBound{}, nil
	}
	names, err := s.QueueClosure()
	if err != nil {
		return store.QueueBound{}, err
	}
	return store.BoundTo(names), nil
}

// estimate derives how long a pool's queued work takes at its rate, or says why it cannot.
func (e estimator) estimate(k work.PoolKind, perHour *int) (*Estimate, string) {
	var why string
	switch {
	case k.Pool == work.PoolProbing && e.queued.MovingReferences == 0:
		why = "No analyzed reference can move yet, so the sweep has nothing to ask about."
	case k.Pool == work.PoolFetching && k.Unit == work.UnitGit && e.queued.QueuedReferences == 0:
		why = "Nothing is waiting to be fetched."
	case k.Pool == work.PoolAdvisories && e.packages == 0:
		why = "The graph names no action to read advisories for yet."
	case k.Pool == work.PoolPolling && e.polling.References == 0:
		why = "No external reference that can move has been analyzed yet, so polling has nothing to ask about."
	case k.Pool == work.PoolProbing, k.Pool == work.PoolFetching && k.Unit == work.UnitGit,
		k.Pool == work.PoolAdvisories, k.Pool == work.PoolPolling:
	case k.Pool == work.PoolEvents:
		return nil, "No estimate: an event is followed up when it arrives, so no queue waits behind this rate."
	case k.Pool == work.PoolEnumeration:
		return nil, "No estimate: what reading an account costs is known only once it has been read."
	case k.Pool == work.PoolFetching:
		return nil, "No estimate: a listing is read only where it could replace a clone, which is not known before the reference is looked up."
	case k.Pool == work.PoolLocks:
		return nil, "No estimate: a verification runs only when somebody asks for one."
	case k.Pool == work.PoolConsistency:
		return nil, "No estimate: the checker reads a sample each run rather than draining a queue, so its rate " +
			"sets how often a run can go, not when anything finishes."
	default:
		return nil, "No estimate: what a dependency costs to analyze is not known before it is built."
	}
	if why != "" {
		return nil, why
	}
	if perHour == nil {
		return nil, "No rate is set, so no time follows from one: this work goes as fast as its answers come back."
	}
	rate := fmt.Sprintf("At %s %s an hour", grouped(*perHour), k.Unit)
	per := func(units int) time.Duration {
		return time.Duration(float64(units) / float64(*perHour) * float64(time.Hour)).Round(time.Second)
	}
	var est Estimate
	switch k.Pool {
	case work.PoolProbing:
		n := e.queued.MovingReferences
		d := per(n)
		est.Text = fmt.Sprintf("%s, each of the %s references that can move would be asked about again within about %s",
			rate, grouped(n), about(max(d, e.floor)))
		if e.floor > d {
			est.Text += ", since none is asked about sooner"
		}
		est.Text += ". An estimate from the rate, not a promise: it assumes the sweep is running, " +
			"and the sweep does not put the most used first."
		est.Seconds, est.Work = int64(max(d, e.floor).Seconds()), n
	case work.PoolFetching:
		n := e.queued.QueuedReferences
		d := per(2 * n)
		est.Text = fmt.Sprintf("%s, the %s references waiting would each be fetched within about %s, "+
			"at up to two requests apiece. An estimate from the rate, not a promise.", rate, grouped(n), about(d))
		est.Seconds, est.Work = int64(d.Seconds()), n
	case work.PoolAdvisories:
		requests := (e.packages + advisory.DefaultBatchSize - 1) / advisory.DefaultBatchSize
		d := per(requests)
		est.Text = fmt.Sprintf("%s, one refresh of advisories for the %s actions the graph names takes at least %s "+
			"requests, about %s. An estimate from the rate, not a promise.",
			rate, grouped(e.packages), grouped(requests), about(d))
		est.Seconds, est.Work = int64(d.Seconds()), e.packages
	case work.PoolPolling:
		est = e.pollingEstimate(rate, *perHour, per)
	}
	est.Checked = freshnessChecked()
	return &est, ""
}

// pollingEstimate names the reach the rate keeps within the floor, because polling asks the most used first.
func (e estimator) pollingEstimate(rate string, perHour int, per func(int) time.Duration) Estimate {
	n := e.polling.References
	all := max(per((n+poll.DefaultBatch-1)/poll.DefaultBatch), poll.DefaultFloor)
	withinFloor := int(float64(perHour)*poll.DefaultFloor.Hours()) * poll.DefaultBatch
	reach, asked := 0, 0
	for _, r := range e.polling.ByReach {
		if asked += r.References; asked > withinFloor {
			break
		}
		reach = r.Reach
	}
	var text string
	switch {
	case withinFloor >= n:
		text = fmt.Sprintf("%s, each of the %s external references that can move is asked about again within about %s, "+
			"since none is asked sooner", rate, grouped(n), about(all))
	case reach == 1:
		text = fmt.Sprintf("%s, every reference a starting point runs is asked about again within about %s; "+
			"all %s external references that can move, within about %s", rate, about(poll.DefaultFloor), grouped(n), about(all))
	case reach > 1:
		text = fmt.Sprintf("%s, references used by %s or more starting points are asked about again within about %s; "+
			"all %s external references that can move, within about %s",
			rate, grouped(reach), about(poll.DefaultFloor), grouped(n), about(all))
	default:
		text = fmt.Sprintf("%s, the %s external references that can move are each asked about again within about %s, "+
			"those the most starting points run first", rate, grouped(n), about(all))
	}
	text += fmt.Sprintf(". An estimate from the rate, not a promise: it assumes polling is running, which it is while "+
		"discovery is, and that a query of %d references costs a point.", poll.DefaultBatch)
	return Estimate{Text: text, Seconds: int64(all.Seconds()), Work: n}
}

// freshnessChecked names what measures an estimate, from the checker's own catalog.
func freshnessChecked() string {
	for _, c := range checks.Catalog {
		if c.Number != 10 {
			continue
		}
		if c.BuiltBy != "" {
			return fmt.Sprintf("Nothing has measured this yet: the consistency checker's check %d will, "+
				"and is not built.", c.Number)
		}
		return fmt.Sprintf("The consistency checker's check %d measures this.", c.Number)
	}
	return "Nothing has measured this yet."
}

// about rounds a duration to the unit a reader would say it in.
func about(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "a minute"
	case d < 90*time.Minute:
		return spell(int(d.Round(time.Minute)/time.Minute), "minute")
	case d < 48*time.Hour:
		return spell(int(d.Round(time.Hour)/time.Hour), "hour")
	default:
		return spell(int(d.Round(24*time.Hour)/(24*time.Hour)), "day")
	}
}

func spell(n int, unit string) string {
	return strconv.Itoa(n) + " " + plural(int64(n), unit, unit+"s")
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// grouped writes a count with thousands separators, as the interface writes every other count.
func grouped(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
