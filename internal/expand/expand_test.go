package expand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// fixtureEdges is real `codeql bqrs decode --format=json` output. Regenerate with:
//
//	codeql database create --language=actions \
//	    --source-root=ql/actions/abom/test/AbomEdges /tmp/abom-fixture-db
//	codeql query run --database=/tmp/abom-fixture-db --output=/tmp/edges.bqrs \
//	    ql/actions/abom/src/AbomEdges.ql
//	codeql bqrs decode --format=json \
//	    --output=internal/expand/testdata/abom-edges.json /tmp/edges.bqrs
//
// Hand-written BQRS is not a substitute. Edge identity includes source location, so invented
// tuples that share a line silently collapse into one edge and hide whatever is under test.
const fixtureEdges = "testdata/abom-edges.json"

var errTransient = errors.New("connection reset")

// harness wires an Expander onto fakes so the orchestration is exercised without git or CodeQL,
// while the query results it ingests stay real.
type harness struct {
	t        *testing.T
	store    *store.Store
	queue    *frontier.Queue
	expander *Expander

	mu       sync.Mutex
	fetched  []string
	built    []string
	fetchErr map[string]error
}

func (h *harness) Fetch(_ context.Context, dest, owner, repo string) (Tree, error) {
	h.mu.Lock()
	full := owner + "/" + repo
	h.fetched = append(h.fetched, full)
	err := h.fetchErr[full]
	h.mu.Unlock()
	if err != nil {
		return Tree{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Tree{}, err
	}
	return Tree{Dir: dest, Ref: "HEAD", SHA: "sha-" + repo}, nil
}

func (h *harness) CreateDatabase(_ context.Context, srcDir, dbDir, _ string) error {
	h.mu.Lock()
	h.built = append(h.built, srcDir)
	h.mu.Unlock()
	return os.MkdirAll(dbDir, 0o755)
}

func (h *harness) RunQuery(_ context.Context, _, _, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outputPath, []byte("bqrs"), 0o644)
}

// decode stands in for the CodeQL decoder by serving the recorded query output.
func (h *harness) decode(_ context.Context, _, jsonPath string) error {
	body, err := os.ReadFile(fixtureEdges)
	if err != nil {
		return err
	}
	return os.WriteFile(jsonPath, body, 0o644)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	h := &harness{
		t: t, store: s,
		queue:    &frontier.Queue{Store: s, MaxDepth: 3},
		fetchErr: map[string]error{},
	}
	w := store.NewWriter(s, 0)
	h.expander = &Expander{
		Queue:    h.queue,
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Fetcher:  h,
		Builder:  h,
		CodeQL:   h,
		Decode:   h.decode,
		// Job failures are otherwise silent, which hides consume-phase errors behind an
		// unrelated-looking assertion.
		OnEvent: func(ev work.Event) {
			if ev.Kind == work.EventFailed {
				t.Logf("job %s failed: %v", ev.Job.Repository, ev.Err)
			}
		},
	}
	return h
}

func (h *harness) run(limit int) Stats {
	h.t.Helper()
	stats, err := h.expander.Run(context.Background(), Options{
		QueryPath: "AbomEdges.ql", QueryID: "abom/edges",
		WorkDir: h.t.TempDir(), Limit: limit,
	})
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return stats
}

func (h *harness) enqueue(items ...frontier.Item) {
	h.t.Helper()
	if _, err := h.queue.Enqueue(items); err != nil {
		h.t.Fatalf("Enqueue: %v", err)
	}
}

func item(owner, repo, subpath string, depth int) frontier.Item {
	key := owner + "/" + repo
	if subpath != "" {
		key += "/" + subpath
	}
	return frontier.Item{NodeKey: key, Owner: owner, Repo: repo, Subpath: subpath, Depth: depth}
}

func statusOf(t *testing.T, s *store.Store, key string) (status, sha string, attempts int) {
	t.Helper()
	if err := s.DB().QueryRow(
		`SELECT status, COALESCE(resolved_sha,''), attempts FROM abom_frontier WHERE node_key=?`,
		key).Scan(&status, &sha, &attempts); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return status, sha, attempts
}

func queuedDepth(t *testing.T, s *store.Store, key string) (int, bool) {
	t.Helper()
	var depth int
	if err := s.DB().QueryRow(
		`SELECT depth FROM abom_frontier WHERE node_key=?`, key).Scan(&depth); err != nil {
		return 0, false
	}
	return depth, true
}

// Several actions commonly live in one repository, and cloning plus building a database once
// per action would repeat the most expensive part of the work for no benefit.
func TestExpandBatchesItemsSharingARepository(t *testing.T) {
	h := newHarness(t)
	h.enqueue(
		item("gradle", "actions", "setup-gradle", 1),
		item("gradle", "actions", "dependency-submission", 1),
		item("gradle", "actions", "wrapper-validation", 1),
	)

	stats := h.run(10)

	if len(h.fetched) != 1 || h.fetched[0] != "gradle/actions" {
		t.Errorf("fetched = %v, want a single gradle/actions checkout", h.fetched)
	}
	if len(h.built) != 1 {
		t.Errorf("built %d databases, want 1", len(h.built))
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1 repository", stats.Processed)
	}
	if stats.Items != 3 {
		t.Errorf("Items = %d, want 3 dependencies settled", stats.Items)
	}
}

// Every item in a batch is answered by the one analysis, so all of them must be recorded
// against the commit that analysis actually saw.
func TestExpandCompletesEveryItemWithTheResolvedSHA(t *testing.T) {
	h := newHarness(t)
	h.enqueue(
		item("gradle", "actions", "setup-gradle", 1),
		item("gradle", "actions", "dependency-submission", 1),
	)

	h.run(10)

	for _, key := range []string{
		"gradle/actions/setup-gradle", "gradle/actions/dependency-submission",
	} {
		status, sha, _ := statusOf(t, h.store, key)
		if status != frontier.StatusDone {
			t.Errorf("%s status = %q, want done", key, status)
		}
		if sha != "sha-actions" {
			t.Errorf("%s resolved_sha = %q, want sha-actions", key, sha)
		}
	}
}

// referencedFromSeed records a repository under review depending on the given node, which is
// what makes an upstream repository's contents part of a supply chain worth following.
func (h *harness) referencedFromSeed(seedRepo, targetKey string) {
	h.t.Helper()
	id, err := h.store.UpsertRepository(seedRepo, "")
	if err != nil {
		h.t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(h.store, 0)
	workflow := seedRepo + "/.github/workflows/ci.yml"
	step := seedRepo + "-step"
	for _, n := range []store.Node{
		{Key: targetKey, Kind: "workflow", IsExternal: true},
		{Key: workflow, Kind: "workflow", RepositoryID: id},
		{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow},
	} {
		if err := w.AddNode(n); err != nil {
			h.t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: seedRepo + "-uses", SrcKey: step, DstKey: targetKey,
		Kind: "job-uses-reusable-workflow", FilePath: ".github/workflows/ci.yml",
		StartLine: 7, RepositoryID: id,
	}); err != nil {
		h.t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		h.t.Fatalf("Flush: %v", err)
	}
}

// This is the recursive step: what an analysed dependency itself depends on becomes the next
// round of work, which is how the graph reaches past the seed repositories.
func TestExpandEnqueuesNewlyDiscoveredDependencies(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/workflows/main.yml")

	stats := h.run(10)

	// Every external target the referenced workflow pulls in.
	for _, want := range []string{
		"actions/checkout", "actions/setup-node", "actions/upload-artifact",
		"actions/stale", "actions/labeler",
	} {
		depth, ok := queuedDepth(t, h.store, want)
		if !ok {
			t.Errorf("%s was not enqueued", want)
			continue
		}
		if depth != 2 {
			t.Errorf("%s depth = %d, want one past its discoverer", want, depth)
		}
	}
	if stats.Discovered == 0 {
		t.Error("Discovered = 0")
	}
}

// An upstream repository's own CI is not part of any consumer's supply chain, so following its
// dependencies would spend expansion budget on repositories nobody under review depends on.
func TestExpandIgnoresDependenciesOfUnreferencedUpstreamCI(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	stats := h.run(10)

	if stats.Discovered != 0 {
		t.Errorf("Discovered = %d, want none: nothing references this repository's workflows",
			stats.Discovered)
	}
	if _, ok := queuedDepth(t, h.store, "actions/checkout"); ok {
		t.Error("queued a dependency reachable only through an upstream repository's own CI")
	}
}

// The fixture references the same action at two different refs. Identity is the action, not the
// ref, so it is one unit of work rather than two.
func TestExpandQueuesAnActionOnceAcrossRefs(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/workflows/main.yml")

	h.run(10)

	var n int
	if err := h.store.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE node_key='actions/checkout'`).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Errorf("actions/checkout queued %d times, want 1", n)
	}
}

// A container image is a real dependency but nothing git can clone, so it must not enter a
// queue whose only tool is a checkout.
func TestExpandDoesNotQueueUncloneableTargets(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/workflows/main.yml")

	h.run(10)

	rows, err := h.store.DB().Query(`SELECT node_key FROM abom_frontier`)
	if err != nil {
		t.Fatalf("reading queue: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(key, "docker") || strings.HasPrefix(key, "./") {
			t.Errorf("%q should not be queued for checkout", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// Depth bounds the expansion, so discoveries beyond the bound must not enter the queue at all.
func TestExpandStopsEnqueueingAtMaxDepth(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "deep", "", 3)) // MaxDepth is 3
	h.referencedFromSeed("org/app", "octo/deep/.github/workflows/main.yml")

	h.run(10)

	if _, ok := queuedDepth(t, h.store, "actions/checkout"); ok {
		t.Error("a dependency past the depth bound was enqueued")
	}
}

// A deleted or private repository cannot be analysed however often it is tried.
func TestExpandMarksUnreachableRepositoriesTerminal(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "gone", "", 1))
	h.fetchErr["octo/gone"] = fmt.Errorf("%w: cloning", ErrUnavailable)

	stats := h.run(10)

	status, _, _ := statusOf(t, h.store, "octo/gone")
	if status != frontier.StatusUnavailable {
		t.Errorf("status = %q, want unavailable", status)
	}
	if stats.Unavailable != 1 {
		t.Errorf("Unavailable = %d, want 1", stats.Unavailable)
	}
}

// A repository with no workflows and no manifest is a permanent answer too, even though it
// arrives from the extractor rather than from git.
func TestExpandTreatsEmptyExtractionAsTerminal(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "empty", "", 1))
	h.expander.Builder = builderFunc(func(context.Context, string, string, string) error {
		return fmt.Errorf("building: %w", runner.ErrNoSourceCode)
	})

	h.run(10)

	if status, _, _ := statusOf(t, h.store, "octo/empty"); status != frontier.StatusUnavailable {
		t.Errorf("status = %q, want unavailable", status)
	}
}

// A network blip must not be mistaken for a permanent answer, or the dependency would be
// dropped from the graph for good.
func TestExpandRetriesTransientFailures(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "flaky", "", 1))
	h.fetchErr["octo/flaky"] = errTransient

	stats := h.run(10)

	status, _, attempts := statusOf(t, h.store, "octo/flaky")
	if status != frontier.StatusPending {
		t.Errorf("status = %q, want pending for a later retry", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

// Every analysis has to be attributable to the query and commit that produced it.
func TestExpandRecordsProvenance(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	h.run(10)

	var queryID, commit string
	if err := h.store.DB().QueryRow(`
		SELECT query_id, COALESCE(db_commit,'')
		FROM abom_query_run
		WHERE repository = 'octo/composite'`).Scan(&queryID, &commit); err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	if queryID != "abom/edges" {
		t.Errorf("query_id = %q", queryID)
	}
	if commit != "sha-composite" {
		t.Errorf("db_commit = %q, want the analysed commit", commit)
	}
}

// Claiming marks work running, so a limit keeps a round bounded and leaves the rest for later.
func TestExpandRespectsTheClaimLimit(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("a", "one", "", 1), item("b", "two", "", 1), item("c", "three", "", 1))

	h.run(2)

	done := 0
	for _, key := range []string{"a/one", "b/two", "c/three"} {
		if status, _, _ := statusOf(t, h.store, key); status == frontier.StatusDone {
			done++
		}
	}
	if done != 2 {
		t.Errorf("%d of the seeded items completed, want 2", done)
	}
}

// Checkouts are the expansion's outbound requests, so they have to go through the rate cap
// rather than running as fast as the worker pool allows.
func TestExpandPacesCheckouts(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("a", "one", "", 1), item("b", "two", "", 1), item("c", "three", "", 1))

	const perHour = 360000 // a 10ms interval, so three checkouts cannot finish instantly
	h.expander.Pace = work.NewLimiter(perHour)

	start := time.Now()
	h.run(10)
	elapsed := time.Since(start)

	if want := 2 * (time.Hour / perHour); elapsed < want {
		t.Errorf("three paced checkouts took %v, want at least %v", elapsed, want)
	}
}

func TestExpandWithNothingQueuedDoesNoWork(t *testing.T) {
	h := newHarness(t)
	stats := h.run(10)
	if stats.Processed != 0 || len(h.fetched) != 0 {
		t.Errorf("did work with an empty queue: %+v %v", stats, h.fetched)
	}
}

type builderFunc func(ctx context.Context, srcDir, dbDir, language string) error

func (f builderFunc) CreateDatabase(ctx context.Context, srcDir, dbDir, language string) error {
	return f(ctx, srcDir, dbDir, language)
}
