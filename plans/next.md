# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the
one-line reason for it.

- Pop implemented plans to [`plans/implemented.md`](implemented.md), which is git-ignored on
  purpose — git history is the record of what shipped, and this would be a second copy of it.
- Re-rank `Ranked next` as needed, and as we learn from each implemented plan.
- Keep it terse. If an entry needs a paragraph, the paragraph belongs in its plan.
- **One entry is one piece of work, never a phase of one.** A plan with a cheap half and an
  expensive half is sequencing, and sequencing belongs inside the plan. Splitting it here spends
  two slots on one plan and pushes everything below it down twice, which is how a plan that was
  nearly next ends up mid-table without anyone deciding it should.

The ranking is itself a plan, and plans are always subject to change over time.

## Ranked next

Entry 1 is a correctness defect in the one table everything else trusts, found by the operator
reading their own Settings page. Entries 2 to 6 come from the interface audit of 2026-09-07, which
looked at every surface the re-architecture changed. The verdict there: **the machinery landed and
the interface did not follow it.** Repository enumeration is unreachable from the server, pruning
has no surface at all, and the Explorer's default view is still the 12,489-node hairball the
closure exists to prevent.

1. **[A declaration nobody made is not a declaration](db/declaration-provenance.md)** — `gh`
   reports **33** accounts for this operator and **neither `facebook` nor `microsoft` is among
   them**, yet both were declared and the bill of materials was rooted in **44 Facebook and
   Microsoft repositories**. The operator owns **1** repository in this corpus. `abom_owned_scope`
   records `declared_at` and nothing about who or how, so a row somebody typed is indistinguishable
   from a statement the operator made — and *contradicted by gh* renders identically to *gh has not
   been asked*. The **role gate has since shipped** (`owner`/`admin` only, 13 of 33 offered), which
   closes the front door; provenance on the row, and the three verification states, remain.
   **Ranked first because every entry below trusts this table, and because the audit and
   `ui/owned-scope-editor.md` both measured against the false declaration without noticing.**
2. **[The first run draws itself](ui/first-run.md)** — enumerating a declared account's
   repositories is an API call, not an analysis: seconds, no CodeQL. `codeql-abom discover --org`
   already does it and is **unreachable from the server**, so a fresh declaration leads to an empty
   Explorer for as long as it takes to analyze repositories at fifteen seconds each. Repository
   nodes already exist as first-class nodes; owner and containment edges are already synthesized.
   This is the frame the arrivals land in.
3. **[The Explorer starts from what you own](ui/closure-first-explorer.md)** — pruning takes the
   corpus from 56,830 nodes to 14,819 and has **no interface**. The default Explorer view is
   **614 owner-groups over 12,489 nodes** — or 3,356 groups by repository, which is worse — a
   saturated hairball in which no edge can be followed end to end, painted almost entirely one
   boundary color because at that scale every edge crosses accounts. Closure by default, empty
   until something is declared, grouping off, pruning in a quiet corner of Discovery. Also makes
   the **already-built** live-update machinery watchable, which is the most persuasive thing this
   product can do.
4. **[One vocabulary for ownership, and one for boundaries](ui/scope-vocabulary.md)** — the strip
   says "204 repositories in scope" on every page while the declared scope is **1 repository**, and
   the Overview still asserts a single organization. Correcting the declaration made the
   contradiction worse, not better: it read 204 against 44 when audited. **"Under review" is
   retired rather than renamed**: it was never a real state, because nothing happens until an
   operator claims ownership. Nine files. Three surfaces also name the boundary classes three
   different ways.
5. **[An expression ABOM could not resolve is not a dependency](db/unresolved-references.md)** —
   `$/.github/actions/ci-setup` is classified `cross-owner`, so ABOM states the organization
   depends on a third-party account named `$`. Small, and the exact class of fabricated verdict the
   first architectural invariant exists to refuse.
6. **[A bill of materials that survives its own size](ui/bom-page-scale.md)** — one unpaginated
   DOM whose size follows the declaration: **2,548** components over the 44 roots it was audited
   with (2.7 MB, too large to read), **1,000** over the one root the corpus honestly holds, and
   **1,042** for `facebook/rocksdb` alone. Nothing caps it. Two different numbers on screen are
   both called components, and an empty filter result reads as "you have no cross-account
   dependencies".
7. **[Collapse `name@ref` back to one node](ui/explorer-references.md)** — per-reference nodes are
   the useful default and now the only behavior. "Which actions do we use at all" is a second
   legitimate reading, and the counts it needs already ride on every node.
8. **[Run the CodeQL update workflow for real](ql/packs.md)** — `update-codeql.yml` and
   `bump-pack-versions.sh` are exercised locally and have never run in Actions.
9. **[Component catalog spike](ui/testing.md)** — BlazingStory, our own, or neither. Timeboxed,
   with criteria written before the code. Its own README says it may be abandoned; that is the
   decision, not a footnote.
10. **[Explorer component coverage](ui/testing.md)** — needs `JSInterop.SetupModule`. Selection
    state has two independent halves, so it is a phase rather than a few tests.
11. **[Observation store hardening](db/observations.md)** — indexes, retention, the subject keys
    that did not join. Wait until read patterns are known; guessing indexes now would be guessing.
12. **[Publish packs to GHCR](ql/packs.md)** — deliberately deferred until much later. The naming
    and versioning landed early so that publishing is never the thing blocked.

### Found while working, not yet planned

- **The Discovery queue table is sorted by score; the crawl now claims by depth.** Since
  `db/freshness-budget.md` made depth the outer key, the order a reader sees in the queue is not
  the order the crawl will work in. The table shows score 25 and 21 at the top, both transitive,
  while the next claim will be a depth-1 row further down.
- **Discovery shows two decompositions of one queue with no relationship stated.** Tier chips read
  owned 19 / direct 1,370 / transitive 1,411; the objective panel reads depth 1: 1,604, depth 2:
  1,515, and so on. They do not sum to the same total and nothing says why.
- **Per-component provenance is in the export and not on screen.** The Bill of Materials states how
  many findings lack a run key, hash or digest, but a reader cannot see which run produced a given
  finding without downloading the JSON.
- **Iterative deepening was assumed to exist and does not.** `db/freshness-budget.md` was written
  as though `db/discovery-boundary.md` had shipped a depth cap that advances; it had not.
  `MaxDepth` is a static flag. Whether a crawl may deepen on its own — and so surprise an operator
  with a bill — is a decision worth its own plan now that the spend it would incur is recorded.
- **`abom_commit_content` is empty on the whole corpus.** `RecordCommitContent` and
  `CommitContentDigest` exist, the table is created, and **0 rows** have ever been written. So
  `indexed_digest` — one of the four links in the provenance chain `db/analysis-ledger.md` built —
  is available for nothing, and the bill of materials has to state its absence rather than carry
  it. Either the pre-check that would populate it never runs, or it runs and never records.
- **2,759 findings have no `query_run_id`,** every one of them `actions/unpinned-tag`. They were
  analyzed and which run produced them was not recorded, so they cannot be reproduced or aged out
  with their pack. One rule out of the suite behaving differently from the rest suggests a single
  ingestion path that does not carry the run through.
- **One `job-needs-job` edge points at an action.** `needs:` names a job in the same workflow, so
  its destination is always a job — 2,994 of 2,995 are. The exception is
  `crate-ci/typos:.github/workflows/post-release.yml:130`, whose destination node is
  `actions/checkout`. Harmless today because `DependencyEdgeFilter` excludes ordering edges from
  every traversal, which is exactly why nothing caught it.

## Implemented

Moved to [`plans/implemented.md`](implemented.md), which is **deliberately git-ignored**. Git
history is already the record of what shipped; a markdown summary of it is a second copy that
drifts from the log it duplicates. The reasoning history does not carry — what the corpus refuted,
which numbers did not move and why that was right — lives there for whoever works here next.

It may not exist on a fresh clone. That means nothing has been recorded on this machine, not that
nothing has shipped.

## Not doing, and why

- **Evaluating the findings lane through the query server.** It splits cleanly — `database
  analyze` is `run-queries` plus `interpret-results`, and running them separately over the same
  tree produces the same SARIF with all 35 rules, so no rule metadata would be assembled by hand.
  It fails on the layout: `interpret-results` finds results by their **pack-relative path**, and
  given a tree it does not recognize it **exits 0, prints "Exported results to SARIF" and writes
  zero rules and zero results** — measured, by flattening a tree that had just produced 12
  findings. Every repository would have come back clean. Reproducing CodeQL's internal layout from
  pack paths is the only way through and getting it wrong is silent, which is not a trade worth
  making on the lane that carries every security verdict. See `db/analysis-ledger.md`.
- **Ingesting the actions CFG as control flow.** `codeql/actions-all@0.6.0` builds it from
  `StandardPreOrderTree` over the AST and **never consults `needs:`** — `grep needs Cfg.qll`
  returns nothing — so sibling jobs are chained in document order. For two independent jobs that
  asserts a sequence that does not exist, since Actions runs jobs in parallel. Its nodes are also
  expressions rather than steps, with edges jumping backwards into `env:` declarations. Measured
  on a real database: an entire workflow is the chain `enter → name → Job → Uses Step → Uses Step
  → exit`. The truthful parts of "what stands between" are `step_index` and conditions, which are
  already held, plus `needs:`, which the CFG cannot supply. See `api/reachability.md`.
- **Evaluating `if:` conditions.** Whether `github.ref == 'refs/heads/main'` holds on a given run
  is not something static analysis settles. Report the expression; let a reader judge.
- **Deriving vulnerability from an advisory.** The observed reference may sit outside the
  vulnerable range. Report the advisory, its range and the refs side by side.
- **A `pin_status` column, or any verdict column.** `TestSchemaHasNoVerdictColumns` guards this.
- **Rewriting the Explorer's renderer, or splitting the server into services, to fix Explorer
  performance.** Both were considered and neither is supported by the measurements: the renderer
  holds 60 FPS at 430 nodes, and every server endpoint answers in under 550 ms. A service
  boundary drawn through code that is already fast moves the same 8 MB across one more hop. If
  either earns its place it will be on its own evidence — see `ui/performance.md`.
