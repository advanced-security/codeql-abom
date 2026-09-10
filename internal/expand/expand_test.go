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
//	.github/skills/regenerate-abom-fixtures/scripts/regen-edges-fixture.sh
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
	probe    *harnessProbe

	mu       sync.Mutex
	fetched  []string
	built    []string
	queried  []string
	fetchErr map[string]error
}

// harnessProbe answers what a reference points at without a fetch, agreeing with what Fetch
// reports so the pre-check and the checkout cannot disagree by construction.
type harnessProbe struct {
	mu      sync.Mutex
	asked   []string
	moved   string
	content string
	failed  bool
}

// state is the commit every reference resolves to and the content found there. Empty commit
// means "whatever the repository's own default is", which is what an unmoved corpus looks like.
func (p *harnessProbe) state() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	content := p.content
	if content == "" {
		content = "name: ci\n"
	}
	return p.moved, content
}

func (p *harnessProbe) Resolve(_ context.Context, owner, repo, ref string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, owner+"/"+repo+"@"+ref)
	if p.failed {
		return "", errTransient
	}
	if p.moved != "" {
		return p.moved, nil
	}
	return "sha-" + repo, nil
}

func (p *harnessProbe) moveEverythingTo(sha string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.moved = sha
}

// rewriteContent changes what a fetch finds, which is what a rebuild has to depend on.
func (p *harnessProbe) rewriteContent(body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content = body
}

func (p *harnessProbe) failEverything() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = true
}

func (h *harness) Fetch(_ context.Context, dest, owner, repo, ref string) (Tree, error) {
	h.mu.Lock()
	full := owner + "/" + repo
	h.fetched = append(h.fetched, full)
	err := h.fetchErr[full]
	sha, content := h.probe.state()
	h.mu.Unlock()
	if err != nil {
		return Tree{}, err
	}
	if err := os.MkdirAll(filepath.Join(dest, ".github", "workflows"), 0o755); err != nil {
		return Tree{}, err
	}
	// Real content, so a commit can move without it changing and the two can be told apart.
	if err := os.WriteFile(
		filepath.Join(dest, ".github", "workflows", "ci.yml"), []byte(content), 0o644); err != nil {
		return Tree{}, err
	}
	if sha == "" {
		sha = "sha-" + repo
	}
	return Tree{Dir: dest, Ref: "HEAD", SHA: sha}, nil
}

func (h *harness) CreateDatabase(_ context.Context, srcDir, dbDir, _ string) error {
	h.mu.Lock()
	h.built = append(h.built, srcDir)
	h.mu.Unlock()
	return os.MkdirAll(dbDir, 0o755)
}

func (h *harness) RunQueries(
	_ context.Context, _ string, queryPaths []string,
) (map[string]string, error) {
	out := make(map[string]string, len(queryPaths))
	dir := h.t.TempDir()
	for _, q := range queryPaths {
		h.mu.Lock()
		h.queried = append(h.queried, q)
		h.mu.Unlock()
		bqrs := filepath.Join(dir, filepath.Base(q)+".bqrs")
		if err := os.WriteFile(bqrs, []byte("bqrs"), 0o644); err != nil {
			return nil, err
		}
		out[q] = bqrs
	}
	return out, nil
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
		probe:    &harnessProbe{},
		fetchErr: map[string]error{},
	}
	w := store.NewWriter(s, 0)
	h.expander = &Expander{
		Queue:    h.queue,
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Fetcher:  h,
		Probe:    h.probe,
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

// enqueue puts items in the queue and declares their accounts.
//
// A directly enqueued item is a root, and since 2026-09-07 roots come from the declared scope —
// `SeedFrontier` produces nothing else. Declaring here is what this helper always meant by "a
// repository under review"; use enqueueUnowned to queue something deliberately outside it.
func (h *harness) enqueue(items ...frontier.Item) {
	h.t.Helper()
	h.own(items...)
	h.enqueueUnowned(items...)
}

// own adds each item's account to the declaration without discarding what is already there.
func (h *harness) own(items ...frontier.Item) {
	h.t.Helper()
	declared, err := h.store.OwnedScope()
	if err != nil {
		h.t.Fatalf("OwnedScope: %v", err)
	}
	seen := map[string]bool{}
	for _, a := range declared {
		seen[strings.ToLower(a.Owner)] = true
	}
	for _, it := range items {
		if it.Owner == "" || seen[strings.ToLower(it.Owner)] {
			continue
		}
		seen[strings.ToLower(it.Owner)] = true
		declared = append(declared, store.DeclaredAccount{
			Owner: it.Owner, Kind: store.AccountOrganization, Enabled: true,
		})
	}
	if err := h.store.SetOwnedScope(declared, store.DeclaredByCLI); err != nil {
		h.t.Fatalf("SetOwnedScope: %v", err)
	}
}

func (h *harness) enqueueUnowned(items ...frontier.Item) {
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

func queuedSupplyChain(t *testing.T, s *store.Store, key string) string {
	t.Helper()
	var chain string
	if err := s.DB().QueryRow(
		`SELECT supply_chain FROM abom_frontier WHERE node_key=?`, key).Scan(&chain); err != nil {
		t.Fatalf("reading supply_chain for %s: %v", key, err)
	}
	return chain
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

// This is the recursive step: what an analyzed dependency itself depends on becomes the next
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

// Local references resolve inside the repository being analyzed rather than becoming new work:
// `./.github/actions/greet` is already part of the tree that was just cloned.
func TestExpandDoesNotQueueLocalReferencesAsNewRepositories(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	h.run(10)

	for _, key := range []string{"./.github/actions/greet", "./.github/workflows/reusable.yml"} {
		if _, ok := queuedDepth(t, h.store, key); ok {
			t.Errorf("%s was enqueued, but a local path names nothing to fetch", key)
		}
	}
}

// A composite action's own steps run inside whoever calls the action, so what they pull in has
// to be followed. Requiring some *other* repository to reference the container first dropped
// these: on the real corpus 93 runtime dependencies were never analyzed, including
// chef/common-github-actions' azure-login pulling lancemccarthy/akeyless-action.
//
// Following is a separate question from how they are labeled, which is why this asserts only
// that they are queued.
func TestExpandFollowsACompositeActionsOwnSteps(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	h.run(10)

	for _, want := range []string{"actions/cache", "actions/setup-python"} {
		if _, ok := queuedDepth(t, h.store, want); !ok {
			t.Errorf("%s was not enqueued: a composite action's steps run for every caller", want)
		}
	}
}

// "Runs for every caller" is a claim about callers, so it needs one. Once a repository under
// review names the composite action, its steps run inside that repository and what they pull in
// is executed here.
func TestACompositeActionsStepsAreRuntimeOnceSomethingUnderReviewCallsIt(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/actions/greet")

	h.run(10)

	for _, want := range []string{"actions/cache", "actions/setup-python"} {
		if chain := queuedSupplyChain(t, h.store, want); chain != frontier.SupplyChainRuntime {
			t.Errorf("%s supply_chain = %q, want %q",
				want, chain, frontier.SupplyChainRuntime)
		}
	}
}

// The same steps, with nobody under review calling the action holding them. Being inside a
// `runs:` block says they run for whoever invokes it, not that anyone here does — and a rule
// that read the edge kind alone carried that claim one hop past its evidence.
func TestACompositeActionNothingUnderReviewCallsIsBuildTime(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	h.run(10)

	if chain := queuedSupplyChain(t, h.store, "actions/cache"); chain != frontier.SupplyChainBuild {
		t.Errorf("supply_chain = %q, want %q while nothing under review calls the action",
			chain, frontier.SupplyChainBuild)
	}
}

// An upstream project's own CI is still followed, because compromising a release pipeline is how
// a consumer gets a malicious version. It is labeled, not skipped: it is not executed by anyone
// under review, and reporting it as though it were would overstate what runs here.
func TestExpandLabelsUpstreamCIAsBuildTime(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))

	h.run(10)

	if _, ok := queuedDepth(t, h.store, "actions/setup-node"); !ok {
		t.Fatal("actions/setup-node was not enqueued from the repository's own CI")
	}
	if chain := queuedSupplyChain(t, h.store, "actions/setup-node"); chain != frontier.SupplyChainBuild {
		t.Errorf("supply_chain = %q, want %q", chain, frontier.SupplyChainBuild)
	}
}

// Once something under review calls the workflow, its steps are executed by that caller, so the
// same dependency is runtime rather than build-time. The stronger claim has to win, or the order
// repositories happen to be analyzed in would decide how a dependency is reported.
func TestExpandUpgradesBuildTimeToRuntimeWhenSomethingCallsIt(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/workflows/main.yml")

	h.run(10)

	if chain := queuedSupplyChain(t, h.store, "actions/setup-node"); chain != frontier.SupplyChainRuntime {
		t.Errorf("supply_chain = %q, want %q once a repository under review calls the workflow",
			chain, frontier.SupplyChainRuntime)
	}
}

// Discovery only enqueues when a repository is analyzed, so a change to what counts as a
// dependency reaches an already-analyzed database only if something re-analyzes it — and on a
// drained queue nothing ever does. The sweep re-applies the current rule to what is already
// known, which is what makes a rule change take effect without re-cloning the world.
func TestSweepEnqueuesWhatTheCurrentRuleNowAdmits(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/actions/greet")
	h.run(10)

	// Forget the discoveries, keeping the analyzed repository and its edges, so the sweep has
	// the same starting point as a database analyzed under an older rule.
	if _, err := h.store.DB().Exec(
		`DELETE FROM abom_frontier WHERE node_key LIKE 'actions/%'`); err != nil {
		t.Fatalf("clearing discoveries: %v", err)
	}
	if _, ok := queuedDepth(t, h.store, "actions/cache"); ok {
		t.Fatal("fixture setup failed: the discovery is still queued")
	}

	n, err := SweepDiscoveries(h.store, h.queue)
	if err != nil {
		t.Fatalf("SweepDiscoveries: %v", err)
	}
	if n == 0 {
		t.Error("the sweep enqueued nothing, so a rule change could never reach an existing database")
	}
	if _, ok := queuedDepth(t, h.store, "actions/cache"); !ok {
		t.Error("actions/cache was not recovered by the sweep")
	}
	if chain := queuedSupplyChain(t, h.store, "actions/cache"); chain != frontier.SupplyChainRuntime {
		t.Errorf("supply_chain = %q, want %q", chain, frontier.SupplyChainRuntime)
	}
}

// Running it twice must not keep finding work, or a drained queue would never stay drained and
// the loop would spin re-offering the same dependencies for ever.
func TestSweepIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.run(10)

	if _, err := SweepDiscoveries(h.store, h.queue); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	n, err := SweepDiscoveries(h.store, h.queue)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("second sweep enqueued %d, want none", n)
	}
}

// Enqueue may only ever strengthen a classification, so tightening the rule cannot on its own
// correct a row an older, looser rule already labeled runtime. On the reference corpus 254 rows
// were stuck that way, and re-crawling would not have moved one of them.
func TestReclassifyingCorrectsALabelAnOlderRuleLeftBehind(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.run(10)

	if _, err := h.store.DB().Exec(
		`UPDATE abom_frontier SET supply_chain = ? WHERE node_key = 'actions/cache'`,
		frontier.SupplyChainRuntime); err != nil {
		t.Fatalf("staging a stale label: %v", err)
	}

	if _, err := ReclassifySupplyChain(h.store, h.queue); err != nil {
		t.Fatalf("ReclassifySupplyChain: %v", err)
	}

	if chain := queuedSupplyChain(t, h.store, "actions/cache"); chain != frontier.SupplyChainBuild {
		t.Errorf("supply_chain = %q, want %q: nothing under review calls the action holding it",
			chain, frontier.SupplyChainBuild)
	}
}

// The fixture references the same action at two different refs. Each ref is its own tree, its own
// database and its own findings, so each is its own unit of work — the action is the identity in
// the graph, and the reference is the identity of the analysis.
func TestExpandQueuesAnActionOncePerRef(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("octo", "composite", "", 1))
	h.referencedFromSeed("org/app", "octo/composite/.github/workflows/main.yml")

	h.run(10)

	rows, err := h.store.DB().Query(
		`SELECT ref FROM abom_frontier WHERE node_key='actions/checkout' ORDER BY ref`)
	if err != nil {
		t.Fatalf("reading refs: %v", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading refs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("actions/checkout queued as %v, want one row per referenced ref", refs)
	}
	if refs[0] == refs[1] {
		t.Errorf("both rows are %q, so the refs were collapsed after all", refs[0])
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

// A deleted or private repository cannot be analyzed however often it is tried.
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
		t.Errorf("db_commit = %q, want the analyzed commit", commit)
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
