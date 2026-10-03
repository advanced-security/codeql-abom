# A graph that knows which commit said what

**Status:** sightings keep what each commit declared, and one walk reads them at the commits a
reference resolves to — reach, with the version of each dependency every repository runs, exposure
and the node page, held to the submission closure; a running crawl re-observes the commits recorded
before sightings kept content, a cycle's claim at a time; every derivation is recomputed wholesale.

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
arrive is [`api/change-ingestion.md`](../api/change-ingestion.md); this plan is what the graph does
with them.

## The model

- **A commit's declarations are immutable.** What CodeQL extracted from a commit's workflows and
  actions never changes, and a commit whose indexed content is identical to an analyzed one declares
  the same things. Sightings carry that per commit; the row is a projection holding the last
  observed version, for the views that show one.
- **What is live changes.** An owned repository is live at its default branch and its protected
  branches, which is where workflows run with secrets and where `pull_request_target` and `schedule`
  read from. A dependency is live at each commit some live declaration's reference resolves to. A
  commit nothing resolves to any more is history: kept, and walked by nothing.
- **Every walk stands at live commits and reads what each declared there.** A sighting whose content
  was never recorded says nothing, so it adds no route rather than borrowing the row's. A live commit
  settled by content equivalence reads the identical commit's sightings.

## Pending

1. **Derive incrementally.** A ref that moves, or a commit observed for the first time, changes the
   reach, exposure and closure of what depends on that version and nothing else. Maintain those
   rather than recomputing the graph: `abom_node_version_reach` already says which repositories run
   each version, so a moved ref names the rows it retracts. The wholesale recompute stays as the
   oracle the [consistency checker](../tools/graph-consistency-checker.md) compares against. An
   entry nothing has measured yet stays unmeasured: incremental maintenance is exactly where a
   missing row turns into a zero.
2. **Know a commit's content without fetching it.** Content equivalence needs a digest of the
   filtered tree, which today comes from a checkout. Git tree object ids are content hashes the API
   returns, so a push that touched no workflow or action file can be settled as identical to its
   parent with one read and no clone. The digest from tree ids must equal the checkout's, proven on
   the corpus before anything settles on it.
3. **Owned repositories live at their protected branches too.** `codeql-abom branches` records which
   branches are protected; the live set reads it, and a branch losing protection takes its commit
   out of every walk.
4. **Decide what the one-version views show.** The Explorer, the inventory units, pinning and the
   bill of materials read the row, which holds the last observed live version. Either they show one
   entry per live version, which is what the walks now answer, or they keep one row and say which
   version it is. Both are honest; showing the row without saying which commit wrote it is not.
   Which version each repository runs is stored, and the details page of an action is the first
   reader to show it, per [`ui/action-drilldown.md`](../ui/action-drilldown.md): on the crawled
   organization 24 dependencies are run at more than one version, `actions/checkout` at 8.
5. **A location's kind and source per commit.** The upsert keeps the first writer's `edge_kind` and
   source node and the last writer's target, so a location that became a different kind of reference
   holds a mixture. Measure how often a sighted location changes kind before deciding whether
   sightings carry it.

## The constraints that must not be broken

**A commit's declarations are its own.** Nothing reads a target, reference or pin comment for one
commit from a row another commit wrote. A sighting without recorded content is unknown, and unknown
adds no route — it is never a guess filled from the row.

**A commit nothing resolves to is not walked.** History is kept for the ledger and walked by
nothing: what a deleted step or an abandoned release declared is not what anybody runs.

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

- Incremental derivation equals the wholesale recompute on every change the checker samples.
- Exposure, blast radius and node reach stay byte-identical across each item wherever nothing was
  re-observed, on a copy of the crawled organization.
