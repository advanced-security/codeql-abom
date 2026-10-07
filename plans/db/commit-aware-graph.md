# A graph that knows which commit said what

**Status:** every walk and every one-version view reads what each commit declared at the commits
that are live — a dependency at what references resolve to, a starting point's own workflows at its
default and protected branches — and reach and exposure are derived for what changed; one reader,
owned-scope membership, still reads the row.

## Why this exists

A repository changes by commit, and ABOM's graph was built as though it changed by location. An
edge is keyed by where it is declared (`repo:path:line:col`), so two commits declaring different
things at one location share a row, and the row holds whichever was analyzed last. Sightings record
which commits saw the location; until they kept content, they could not say what each saw.

**Measured on the crawled organization:** 10,174 rows are sighted at more than one commit, and a
random sample of 300 of them found **149 that differ between commits** — 97 another reference of the
same action, 50 a different action entirely. For each older commit, every walk named the newest
commit's target. `pre-commit/action` at v3.0.0 declares `actions/cache@v3` at `action.yml:15:11` and
at v3.0.1 `@v4`, so `has-ghas/codeql`, which runs v3.0.0, had no route to `actions/cache@v3`.

And nothing here keeps up with commits. Graph facts change only when the crawl re-analyzes
something, aggregates are recomputed over the whole graph, and ABOM's target is now an enterprise:
many organizations, up to 100,000 repositories each, with pushes arriving all day. How the changes
arrive is [built](../../docs/architecture.md#the-pipeline); this plan is what the graph does with
them.

## The model

- **A commit's declarations are immutable.** What CodeQL extracted from a commit's workflows and
  actions never changes, and a commit whose indexed content is identical to an analyzed one declares
  the same things. Sightings carry that per commit; the row holds whichever was analyzed last, and
  nothing that answers about a commit reads it.
- **What is live changes.** An owned repository is live at its default branch and its protected
  branches, which is where workflows run with secrets and where `pull_request_target` and `schedule`
  read from. A dependency is live at each commit some live declaration's reference resolves to. A
  commit nothing resolves to any more is history: kept, and walked by nothing.
- **Every walk stands at live commits and reads what each declared there.** A sighting whose content
  was never recorded says nothing, so it adds no route rather than borrowing the row's. A live commit
  settled by content equivalence reads the identical commit's sightings.

## Pending

1. **Owned-scope membership reads the row.** `scopeSets` and `scopeNodes` decide what the declared
   repositories' own lines name from the row's target, which is whichever commit was analyzed last.
   It should read their declarations at their starting commits (`store.StartingCommits`), as the
   walk does. Measured on the crawled organization this moves nothing: all 164 owned rows were
   written at their repository's starting commit, so the row and the declaration agree. It becomes
   wrong the first time a declared repository is analyzed at a commit nothing resolves to after its
   default branch's. The clean route is a temp table built from `StartingCommits` once per database
   state, read by `scopeSets`; eight scoped reads (graph nodes and edges, the inventory page,
   repositories, workflows, jobs, relationships, finding review) and the node-cost plan test would
   build it first, and during a crawl that is a few hundred milliseconds on most owned requests.
   Do it when that cost has a home — the incremental derivation can maintain the table instead of
   each read rebuilding it.
2. **Upstream projects' own CI is shown where nobody runs it.** A starting point's own workflows are
   live only at its default and protected branches, but an upstream repository's are still read at
   every commit a consumer's reference resolves to, which runs its `action.yml` and never its
   `.github/workflows`. Walks are unaffected, because upstream CI never counts as a dependent. The
   views are not: the bill of materials lists **861** upstream workflow components, and
   Relationships and the whole-graph Explorer draw their lines. A workflow another repository calls as
   a reusable workflow stays live at its callers' commits, as it does for starting points. Measure
   what leaves each view before deciding whether any of it should stay as context.

   **The bill of materials does it by design, and it is worst for one repository.**
   `store.walkOutward` continues from every component into every node of that component's
   repository — how the walk reaches a composite action's own steps — and so into the repository's
   CI, that CI's dependencies, and theirs. `has-ghas/test-repo-2`, one workflow with one `uses:` of
   a composite action naming three actions, gets a document of **1,000 components and 2,260
   relationships, 775 of them other repositories' CI workflows, a dozen hops deep**, where about six
   actions are what runs. Follow what a component contains, not what its repository holds.

## The constraints that must not be broken

**A sighting does not carry its location's kind or source.** The upsert keeps the first writer's
`edge_kind` and source node, so a location that changed between `needs:` and `uses:` holds a
mixture. On the crawled organization that is **4 of 10,981** locations sighted at more than one
commit, all in `github/codeql-action`'s own CI, none reachable from a starting point. The other 520
whose target changed kind went between an action, a composite action and an image, which are all
still a step's `uses:`. If a location in a repository a starting point reaches ever changes kind,
sightings carry it.

**A commit's declarations are its own.** Nothing reads a target, reference or pin comment for one
commit from a row another commit wrote. A sighting without recorded content is unknown, and unknown
adds no route — it is never a guess filled from the row.

**A commit nothing resolves to is not walked.** History is kept for the ledger and walked by
nothing: what a deleted step or an abandoned release declared is not what anybody runs.

**A repository's own CI runs where it is a starting point, not where it is a dependency.** A
consumer pinning `docker/build-push-action@v5` runs its `action.yml` at that commit, never its
`.github/workflows`. Its own workflows are read at its default and protected branches only, and a
repository the queue knows only as somebody's dependency contributes nothing as a starting point.
On the crawled organization two seeds were read at 42 release commits that way, and
`actions/checkout`'s depending steps counted 102 steps of CI nobody runs.

**A reference with no recorded commit constrains nothing.** An unanalyzed version is something ABOM
has no reading of, not a version with no children; any constraint inherits that, or it silently
drops real dependents on a corpus still crawling.

**Walks over one graph agree or differ on purpose.** Reach, exposure and the node page are one walk,
and the submission closure answers the same question from the other end, in SQL. Tests hold them
together, over every node of every fixture that binds a version; a derivation that quietly differs
is found only when two figures cannot both be right.

**A snapshot is never built from what is not known.** A submission replaces what GitHub found, so a
closure reading a commit whose declarations are unknown is refused, and the preview says which
commits before anybody presses.

## Verification

- With a declared repository's row written at a commit nothing resolves to, "what you own" lists
  what its default branch declares, and every owned figure on the crawled organization is unchanged.
- Incremental derivation still equals the wholesale recompute.
