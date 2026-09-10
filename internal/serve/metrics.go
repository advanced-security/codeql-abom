package serve

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// latencyBucketsMillis are the upper bounds of the fixed histogram, in milliseconds.
//
// Fixed rather than computed: an exact percentile needs every sample retained, which is an
// unbounded allocation on the request path. These bounds bracket the measured spread of this
// server — most endpoints answer under 10 ms and the graph and queue answer near 700 ms.
var latencyBucketsMillis = []int64{1, 5, 10, 50, 100, 500, 1000, 5000}

// unmatchedRoute buckets requests no pattern claimed. Keying those on the URL would open a bucket
// per bad path, which is the unbounded growth the pattern key exists to avoid.
const unmatchedRoute = "(unmatched)"

// recentSamples bounds the ring. Enough to see what a page load just did, not a history.
const recentSamples = 64

// RouteStat is what one route has cost since the process started.
type RouteStat struct {
	Route string `json:"route"`
	Count int64  `json:"count"`
	// Errors counts responses of 400 and above, so a fast route that is failing is not read as
	// a healthy one.
	Errors      int64 `json:"errors"`
	TotalMillis int64 `json:"totalMillis"`
	MaxMillis   int64 `json:"maxMillis"`
	Bytes       int64 `json:"bytes"`
	// MarshalMillis separates encoding from everything else. Measured 2026-09-11 at 1.4% of
	// `/api/graph/full` and absent from `/api/frontier`, against 96% and 60% in `sqlite3_step`
	// under cgo — so this exists to notice if that stops being true, not because it costs today.
	MarshalMillis int64 `json:"marshalMillis"`
	// MarshalKnown separates "encoded nothing" from "encoded instantly". A pprof page, a UI asset
	// and a 404 all answer without marshaling, and reporting 0 ms for them asserts a measurement
	// nobody took.
	MarshalKnown bool `json:"marshalKnown"`
	// Buckets holds one count per latencyBucketsMillis bound, plus a final overflow bucket.
	Buckets []int64 `json:"buckets"`
}

// RequestSample is one request, kept so a reader can see what just happened rather than only what
// the totals became.
type RequestSample struct {
	At     string `json:"at"`
	Route  string `json:"route"`
	Status int    `json:"status"`
	Millis int64  `json:"millis"`
	Bytes  int64  `json:"bytes"`
}

// Metrics is what the server has served, as a snapshot a reader can be handed.
type Metrics struct {
	Routes []RouteStat     `json:"routes"`
	Recent []RequestSample `json:"recent"`
	Bounds []int64         `json:"bucketBoundsMillis"`
	// Runtime answers "is it growing", which no per-route figure can.
	Runtime RuntimeStat `json:"runtime"`
	// Delta answers "is the incremental path actually incremental", which latency cannot: a
	// permanent fallback to the whole payload is fast enough to look healthy.
	Delta DeltaStat `json:"delta"`
}

// DeltaStat counts what the incremental path did, rather than how long it took.
//
// A reset is legitimate — an unknown cursor, or one from before a compaction — so the count is not
// an error count. What it makes visible is a *ratio* that stops changing: a client polling forever
// and never being honored is reloading the whole graph every time, and nothing else says so.
type DeltaStat struct {
	Honored int64 `json:"honored"`
	Reset   int64 `json:"reset"`
}

// RuntimeStat is the process, not the request.
type RuntimeStat struct {
	Goroutines   int    `json:"goroutines"`
	HeapAllocMiB uint64 `json:"heapAllocMiB"`
	HeapSysMiB   uint64 `json:"heapSysMiB"`
	GCCycles     uint32 `json:"gcCycles"`
	UptimeSecs   int64  `json:"uptimeSeconds"`
}

// metrics accumulates request cost in memory.
//
// In memory and not in SQLite, deliberately. The store permits one writer and the crawl is it;
// making the read path write a row per request would put serving in contention with ingest and
// turn a measurement into a cause of the thing it measures.
type metrics struct {
	mu      sync.Mutex
	byRoute map[string]*RouteStat
	recent  []RequestSample
	next    int
	started time.Time
	delta   DeltaStat
}

func newMetrics() *metrics {
	return &metrics{
		byRoute: map[string]*RouteStat{},
		recent:  make([]RequestSample, 0, recentSamples),
		started: time.Now(),
	}
}

// recordDelta counts one answer from the incremental path.
func (m *metrics) recordDelta(reset bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reset {
		m.delta.Reset++
		return
	}
	m.delta.Honored++
}

// record adds one request. Called on every response, so it allocates nothing per call once a
// route has been seen once.
//
// A negative marshalMillis means the handler encoded nothing, which is not the same as encoding
// that took no measurable time.
func (m *metrics) record(route string, status int, millis, bytes, marshalMillis int64) {
	if route == "" {
		route = unmatchedRoute
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	stat := m.byRoute[route]
	if stat == nil {
		stat = &RouteStat{Route: route, Buckets: make([]int64, len(latencyBucketsMillis)+1)}
		m.byRoute[route] = stat
	}
	stat.Count++
	stat.TotalMillis += millis
	stat.Bytes += bytes
	if marshalMillis >= 0 {
		stat.MarshalMillis += marshalMillis
		stat.MarshalKnown = true
	}
	if millis > stat.MaxMillis {
		stat.MaxMillis = millis
	}
	if status >= 400 {
		stat.Errors++
	}
	stat.Buckets[bucketOf(millis)]++

	sample := RequestSample{
		At:     time.Now().UTC().Format(time.RFC3339),
		Route:  route,
		Status: status,
		Millis: millis,
		Bytes:  bytes,
	}
	if len(m.recent) < recentSamples {
		m.recent = append(m.recent, sample)
		return
	}
	m.recent[m.next] = sample
	m.next = (m.next + 1) % recentSamples
}

func bucketOf(millis int64) int {
	for i, bound := range latencyBucketsMillis {
		if millis <= bound {
			return i
		}
	}
	return len(latencyBucketsMillis)
}

// snapshot copies the aggregates, most expensive first.
//
// A route nobody called is absent rather than present with a zero, the same way an unmeasured
// node is not one with a reach of zero.
func (m *metrics) snapshot() []RouteStat {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]RouteStat, 0, len(m.byRoute))
	for _, stat := range m.byRoute {
		copied := *stat
		copied.Buckets = append([]int64(nil), stat.Buckets...)
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMillis != out[j].TotalMillis {
			return out[i].TotalMillis > out[j].TotalMillis
		}
		return out[i].Route < out[j].Route
	})
	return out
}

// report is the whole answer, aggregates and ring and process together.
func (m *metrics) report() Metrics {
	routes := m.snapshot()

	m.mu.Lock()
	recent := make([]RequestSample, 0, len(m.recent))
	// Oldest first, so the ring reads as a sequence rather than as wherever the cursor stopped.
	for i := range m.recent {
		recent = append(recent, m.recent[(m.next+i)%len(m.recent)])
	}
	started := m.started
	delta := m.delta
	m.mu.Unlock()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return Metrics{
		Routes: routes,
		Recent: recent,
		Bounds: latencyBucketsMillis,
		Delta:  delta,
		Runtime: RuntimeStat{
			Goroutines:   runtime.NumGoroutine(),
			HeapAllocMiB: mem.HeapAlloc / 1024 / 1024,
			HeapSysMiB:   mem.HeapSys / 1024 / 1024,
			GCCycles:     mem.NumGC,
			UptimeSecs:   int64(time.Since(started).Seconds()),
		},
	}
}
