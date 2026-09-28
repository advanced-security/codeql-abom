# One database per owned repository, holding its whole closure

**Status:** An investigation with a measured spike and no implementation. The resolution mechanism
is understood and reproduced; the cost is not measured and may not pay.

## Why this exists

Dataflow stops at every `uses:`. ABOM builds one database per `(repository, reference)` from a
sparse checkout, so `actions/checkout@v4` is analyzed alone and every `uses:` leaving it resolves to
nothing. That bounds what ABOM can say: `uses: ${{ env.ACTION_REF }}` is recorded verbatim and never
resolved, and **a `uses:` built from a remote source is an attack that nothing here can see**.

`codeql/actions-all` already models a `uses:` step as an interprocedural call, and resolves that
call by **string equality**:

```ql
DataFlowCallable viableCallable(DataFlowCall c) { c.getName() = result.getName() }
```

The call side is the `uses:` value truncated at the first `@`. The callable side is

```ql
["", "./"] + relPath.replaceAll(getRepoRoot(), "")
                    .replaceAll("/action.yml", "")
                    .replaceAll(".github/actions/external/", "")
```

So a callee staged at `.github/actions/external/${owner}/${repo}/` matches the caller's `uses:`
string inside the caller's **own** database, with no second repository checked out anywhere. The
namespace this creates is **flat and keyed by `owner/repo`**, which is the fact the whole plan
turns on.

### What the spike measured, and what it refuted

Every line below was reproduced against a CLI-built fixture with `codeql/actions-all` 0.6.1, never
hand-written BQRS.

**Depth is free, and the first reading of this was wrong.** The staging convention was first read
as a first-hop mechanism. It is not: three chained staged composite actions all resolve, and the
upstream production query `actions/code-injection/critical` reports a **seven-step taint path**
from `github.event.pull_request.title` in the root workflow, through three staged `uses:`
boundaries, into the deepest `run:`. Default index filters, no configuration. Nothing in the
mechanism is depth-limited, because a flat namespace has no depth in it.

**Composite actions are indexed by default and reusable workflows are not.** The extractor's
default filters include `**/action.yml` recursively but `.github/workflows/*.yml` only at the
source root. A staged reusable workflow at
`.github/workflows/external/${nwo}/.github/workflows/x.yml` resolves and carries taint **once
indexed**, and is silently absent until a recursive include is appended to `LGTM_INDEX_FILTERS`.

**The bare sibling layout resolves, and is unsafe.** A dependency checked out at `${owner}/${repo}/`
does resolve. But such a checkout carries its own workflows, `getRepoRoot()` is workflow-derived,
and `replaceAll` is global — so the staged repository contributes a repository root of its own. A
root-repository `uses: ./.github/actions/helper` then resolved to **two** callables: its own, and
the dependency's identically-pathed one. At corpus scale, with local action names like `setup`,
`build` and `test`, that is systematic rather than unlucky.

**`.github/reusable_workflows/<nwo>/` is a trap.** The extractor ships a recursive default include
for it, with a source comment saying it exists to let cross-repository workflows be checked out
locally. `actions-all` 0.6.1 models it nowhere. The result is the worst of both: the cross-repository
`uses:` stays unresolved, *and* the staged copy is not excluded from `getRepoRoot()`, so a root
`uses: ./.github/workflows/reusable.yml` for which **no local file exists** bound to a third
party's staged copy and carried a full taint path into it. An extractor/library mismatch that
manufactures a finding out of nothing.

**A database with no workflow file resolves nothing at all.** `getRepoRoot()` requires a `Workflow`
to exist; with none, `getResolvedPath()` is empty for every callable and every `uses:` is
unresolved. A repository that *is* an action resolved zero until one anchor workflow was added,
after which it resolved normally. Silent, and it hits the same class of repository that
`.github/instructions/codeql-queries.instructions.md` already names as invisible to job-only
queries.

**A staged dependency's own `./` local action does not resolve.** Safe rather than wrong, and a
real gap in closure coverage: under `external/` a staged repository contributes no repository root,
so its own relative references have nothing to resolve against.

### The ceiling, and why rooting at an owned repository survives it

**One `owner/repo` per database.** The namespace has no room for a reference, so `@v4` and `@v3` of
the same dependency cannot both be staged. References are ignored entirely — `@v1` against `@v9`
makes no difference to resolution.

Rooting the closure at an **owned repository** is what makes that survivable. Within one
repository's closure a dependency usually appears at one reference, and where it does not the
collision is inside a single owned repository, nameable and reportable, rather than a corpus-wide
approximation. This is why the closure is not rooted at any seed and is not configurable: an owned
repository is also the unit a person can act on, and the unit
[`api/query-simulation.md`](../api/query-simulation.md) needs when it compares a run's results with
the alerts a published pack would raise.

The collision test must count **selected commits**, not only distinct requested refs. Native
lock-aware selection, [now built](../../docs/architecture.md#locks), is a prerequisite: a
workflow can lock an older SHA than its tag currently names, and different contexts can select
different SHAs behind identical ref text. A native lockfile does not enlarge CodeQL's staging
namespace. Any distinct selected revisions of one repository inside a proposed database remain a
collision to report or refuse, not a choice to make by last-write order.

**The whole-account variant is refused for the same reason.** One database over every repository
would have to collapse **132 of 225** dependencies to a single reference each, and a graph
projection built on that is wrong for more than half its rows. The cost argument never justified it
either: the database build is **20.3% of a repository's cost**, and the per-repository read that
actually scales with the organization is the code-scanning probe, which this changes not at all.

## Pending

0. **Measure the collision rate, before anything is built.** Within one owned repository's closure,
   how many dependencies select more than one commit across their governing workflow/lock contexts?
   Count unresolved selection separately; reference spelling alone is insufficient. This decides
   whether the whole shape is sound, and **the honest outcome may be that it stops here**. The
   contextual selection it counts is [recorded](../../docs/architecture.md#locks):
   each consumer's lock at the analyzed commit, its DAG, and a frontier row at every commit a
   lock selects. No lock generation belongs inside this plan.
1. **Measure what a closure costs to extract.** Fewer databases is not less extraction: every owned
   repository re-extracts its whole closure, and closures overlap heavily because every repository
   uses `actions/checkout`. [`db/checkout-cache.md`](../db/checkout-cache.md) removes the fetch
   cost and removes none of the extraction cost. Compare total extraction time against the sum of
   the per-repository times it does *not* replace — this is an addition, not a substitution.
   **A saving is not the justification and never was; the justification is the seven-step path.**
   Depends on 0.
2. **Stage the closure.** The owned repository's own tree at the source root, every member of its
   closure at `.github/actions/external/${owner}/${repo}/`, and reusable workflows at
   `.github/workflows/external/${nwo}/.github/workflows/`. Consume the effective, context-bound
   closure rather than a second selector. `store.SubmissionClosure` currently joins through one
   frontier `resolved_sha`; that is not sufficient for two consumers locking the same ref
   differently. Stage the selected commits on cache misses as well as hits. Depends on 1, the
   [effective selection](../../docs/architecture.md#locks) the submission closure already follows and the existing checkout
   cache.
3. **Index it.** Append a recursive workflow include to `LGTM_INDEX_FILTERS` rather than replacing
   the defaults — the extractor's `autobuild.sh` concatenates a provided value after its own, so a
   replacement would drop `**/action.yml` and silently lose every composite action. Depends on 2.
4. **Anchor it.** Where the owned repository holds no workflow, one is synthesized so
   `getRepoRoot()` has a result. **A synthesized file is not a finding location**: it is excluded
   from ingestion by path, or a rule fires on a file nobody wrote. Depends on 2.
5. **Attribute a result to the repository it came from.** This is the largest item and the one that
   does not exist in any form today. `ingest.Options.Repository` is required and singular, and every
   key `internal/normalize` builds embeds one repository name — `RepoKey`, `WorkflowKey`,
   `CompositeActionKey`, `JobKey`, `StepKey`, `EdgeKey`. One resolver, from a staged source path
   back to `owner/repo` and the path *within* that repository, exercised by the fact that keys built
   this way are **byte-identical** to the keys the per-repository path already produces. Depends
   on 2.
6. **An identity for a database that is not one repository.** `QueryRun.key()` hashes a single
   `Repository` and a single `DBCommit`, and this database has one root and many members. It needs
   a manifest — the set of `(repository, commit)` pairs staged — hashed in their place, with the
   root repository named separately so a run can still be found by the repository it is about.
   Include the governing workflow/lock context and derivation version in the input identity where
   they affect interpretation; do not replace regular upstream query-run reuse with per-consumer
   duplicates. Depends on 5.
7. **Un-stage before findings are linked.** `store.LinkFindingsToNodes` matches on
   `abom_finding.file_path` in all three of its passes. A staged path like
   `.github/actions/external/actions/checkout/action.yml` matches no node in any repository, so
   **every finding from a closure database would attach to nothing** and be invisible to a policy —
   the gap `plans/next.md` already records for composite actions with no `uses:`, arriving again
   for a different reason. Paths are un-staged at ingest, before linking runs. Depends on 5 and 6.

## The constraints that must not be broken

**The closure database is never the discovery engine.** Resumability is the property that a run
dying halfway does not repeat finished work. The resolution plan binds consumers to reusable
dependency/commit work; a newly discovered hop must not force a whole-closure rebuild to discover
the next one.
Per-repository databases stay, keep producing the graph, and keep being what the crawl claims
against. This is a periodic artifact that **reads**, in the same class as `report`, `bom` and
`submit`.

**Bare sibling staging is refused, not corrected.** It resolves, and it makes one local reference
resolve to two callables. The fall-through that once had ABOM reporting third-party dependencies on
an account named `$` is the same class of defect: the fix is to refuse the layout rather than to
patch its output.

**`.github/reusable_workflows/` staging is refused.** It manufactures a resolution and a taint path
where the repository contains no such file. That it is the extractor's own documented convention
does not make it one this library implements.

**Nothing here teaches CodeQL to read a reference from a path.** The staged layout works because the
un-staged path equals the ref-stripped `uses:` string byte for byte. No directory name is ever a
substitute for reading a reference, and nothing built on top of this may assume otherwise.

**A content hash of a database directory never enters `run_key`.** Carried unchanged from
[`db/analysis-ledger.md`](../db/analysis-ledger.md): a CodeQL database is not byte-reproducible, so
a content hash makes every rebuild invalidate every row, the skip silently stops firing, and nothing
looks broken. The manifest hashes **inputs** — the repositories and commits that went in.

**Every row this writes is `extended`, and `extended` is inert.**
[Analysis-mode isolation](../../docs/architecture.md#analysis-modes) is implemented: until
[`db/extended-findings.md`](../db/extended-findings.md) decides otherwise, every aggregate reads
`regular` and nothing this plan produces moves an existing number. **That is this plan's headline
verification, not a caveat on it.**

Per-run sightings preserve shared identities. Conflicting cross-mode edge targets or references
are refused at write time; staging must detect those collisions before persistence, not relax
the guard and replace regular evidence.

**`abom_database_catalog` is not this and may not be reused for it.** That table answers whether
code scanning is enabled on a repository — a fact about configuration, which moves independently of
any commit. These are the Enablement and Coverage questions in
[`docs/vocabulary.md`](../../docs/vocabulary.md), and that file already refuses to let them be
synonyms.

**Extraction over third-party checkouts is safe here for a reason that does not generalize.**
`actions` extraction parses YAML and runs no build, so nothing in a fetched repository executes. The
moment a second language with a build step is added — which
[`ql/semantic-model.md`](semantic-model.md) contemplates — that stops being true and this
constraint has to be re-argued rather than inherited.

**Overlays retire most of this.** `actions` declares no `overlay_support_version`, so
`--overlay-base` is accepted, exits 0 and does nothing;
`TestTheActionsExtractorStillCannotBuildAnOverlayBase` fails when that changes. Overlays are the
supported way to get one database's reach at incremental cost. This plan shares that test and
should be read as the workaround it is.

## Verification

- **The collision measurement is taken and written down before item 2 begins**, including the case
   where it says the shape does not work. Cover identical requested tags with different selected
   commits, not only different tag names.
- Node, edge, reach, bill-of-materials component, submission-closure and pinning totals are
  **byte-identical** before and after, because every row this writes is `extended` and every
   aggregate reads `regular`. Take the baseline after lock-aware resolution has landed, so its
   separately explained corrections are not attributed to staging. Snapshot with
   `sqlite3 abom.db ".backup .abom/before.db"`; `cp` does not read the WAL.
- Keys produced through a closure database are byte-identical to the keys the per-repository path
  produces for the same repository and commit. Any divergence is named, not reconciled.
- The spike's fixture is checked in as a query test and repeated at corpus scale: a staged callee
  resolves and taints its `run:`; a bare-staged one is refused; a `reusable_workflows/`-staged one
  is refused; a tree with no workflow is anchored and then resolves.
- A finding from a closure database attaches to a node in the repository that really holds the file
  it was reported in — the un-staging is verified by attribution, not by string inspection.
- Extraction and evaluation time for one closure, against the sum of the per-repository times it
  runs **in addition to**. A cost that cannot be justified by the seven-step path is a cost this
  plan declines rather than explains.
