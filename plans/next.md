# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the
one-line reason for it.

- Pop implemented plans to `Implemented` section.
- Re-rank `Ranked next` as needed, and as we learn from each implemented plan.
- Keep it terse. If an entry needs a paragraph, the paragraph belongs in its plan.
- **One entry is one piece of work, never a phase of one.** A plan with a cheap half and an
  expensive half is sequencing, and sequencing belongs inside the plan. Splitting it here spends
  two slots on one plan and pushes everything below it down twice, which is how a plan that was
  nearly next ends up mid-table without anyone deciding it should.

The ranking is itself a plan, and plans are always subject to change over time.

## Ranked next

Entries 1 to 6 are one re-architecture, taken in dependency order. The project was seeded from
`gh mrva` databases across 204 repositories nobody owns, and every guardrail below follows from
fixing that: what is owned is declared, discovery is bounded by it, what falls outside is removed,
and what remains is emitted as a bill of materials. The interface entries that were ranked 1 and 2
drop below it, because the re-scope changes the Explorer and doing them first buys rework.

1. **[Ownership is declared, and discovery is gated on it](db/owned-scope.md)** — `origin` defaults
   to `seed` in the schema, so any ingest silently creates a root-set member; and `isOwned` reads
   the 33 accounts `gh` says you belong to, which is membership rather than responsibility.
   Declared orgs with optional per-account include lists, personal account opt-in, `gh` demoted to
   a pre-flight permission check, and no discovery until both are in hand.
2. **[Nothing enters the graph the owned set does not reach](db/discovery-boundary.md)** —
   `Enqueue` drops a candidate only on depth and "names no repository"; there is no ownership
   boundary anywhere. Adds one, renames `seed` to `owned`, and replaces the flat `MaxDepth = 5`
   with iterative deepening so a shallow answer is complete before a deep one is attempted.
3. **[What no longer belongs is removed, provably](db/pruning.md)** — nothing can ever leave, and
   `GraphDelta` can only add, so a client is never told something disappeared. Tombstone, delta
   removals, then compaction behind an epoch. Validated against the wide seed, which is the
   largest orphan set this project will ever have.
4. **[An edge says what boundary it crosses](db/edge-relationships.md)** — same repository, same
   owner, cross owner. Structural and ownership-independent, so it can land any time, and the bill
   of materials needs it. Ranked here so the BOM does not ship and then get reworked.
5. **[A bill of materials you can read, export and verify](api/bill-of-materials.md)** — the
   product. There is no export of any kind today. A closure read from a root, coverage stated
   inside the document rather than beside it, and three serializations: native ABOM JSON,
   CycloneDX 1.6, SPDX 2.3. **SPDX 3.0 was asked for and is unavailable** — `spdx/tools-golang`
   supports 2.1 to 2.3 only.
6. **[Complete first, then current, under budget](db/freshness-budget.md)** — the claim order is
   `score DESC`, which encodes neither completeness nor freshness, and the drift recheck has fired
   **0 times across 2,800 rows**. An explicit objective, `checked_at` made to mean *probed*, and
   per-cycle budget accounting.
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

## Implemented

- **[List every repository using a version](ui/discovery-references.md)** — the grid's "and N more"
  was a dead end on **291 of the corpus's 5,787** name-and-version pairs; it now links to the node
  page, where each row of *References in use* opens the repositories naming that version.
  `NodeUsersOf(key, ref)` backs `GET /api/node/{key}/-/users?ref=`. Cross-checked against the same
  sets counted independently in SQL, exact in all three: `actions/checkout@v4` **285 repositories,
  76 in scope, 7 the operator's**; `actions/upload-artifact@v4` 98 and 43; `actions/setup-node@v3`
  74 and 7. Two numbers a reader would otherwise add up wrongly are now kept apart. **Per-reference
  totals do not sum**: `actions/checkout` has 65 references totalling **1,468** against **1,019**
  distinct repositories, because one repository naming three versions is three rows and one
  repository. And **uses are not repositories**: `gradle/actions` names `checkout@v4` **146** times
  from one repository, and it is `upstream`, so those 146 are nobody's work here — which is why
  every row carries its origin instead of the list being filtered to seeds. The six bUnit tests
  passed the moment they were written, so the render was deliberately broken to watch all six
  fail before being restored. One real defect fell out: a `break` out of a `rows` loop followed by
  another query **deadlocks** under `MaxOpenConns(1)` — it hung for the full ten-minute timeout
  rather than failing, and only on the truncation path, because every other path drains the loop
  and auto-closes.

- **Reprocessing proportional to what changed** — closed out. The pre-check, the fetch-scope
  assertion and the indexed-content digest all landed; overlay databases, the one remaining
  mechanism, are **blocked by the extractor rather than by us**. Overlay support is declared per
  extractor by `overlay_support_version` in `codeql-extractor.yml`; seven bundled extractors
  declare `20250626` and `actions` is not among them. `codeql database create --language=actions
  --overlay-base` still exits **0** and prints *Successfully created database*, then writes
  `overlayBaseDatabase: false` — a silent no-op, which is the worst shape a blocker can take. The
  same command against javascript writes `true` plus an `overlayBaseSpecifier`, which is the
  positive control that the flag works and the extractor is the gate.
  `TestTheActionsExtractorStillCannotBuildAnOverlayBase` asserts it under `make itest` and fails
  when the blocker lifts; it was verified by pointing it at the javascript base and watching it
  fail. Two CLI details cost a round of guessing and are now written down: `--overlay-base` is a
  **boolean** that marks the database being created rather than a path to one, and neither overlay
  option appears in `database create --help`. Closing the plan also settled the cost question it
  had left open — measured on `actions/checkout`, three runs per phase, warm lanes: fetch **3.68 s
  (24.1%)**, build **3.11 s (20.3%)**, graph lane **3.95 s (25.8%)**, findings lane **4.56 s
  (29.8%)**, total **15.30 s**. The build was 3.3 s of 25 s (13%) when first measured; it has not
  got slower, everything around it got faster, so overlays are worth **more** than the ranking
  assumed and nothing at all until the extractor moves. What shipped attacks the other 80%.

- **A commit is recognized by what the extractor would read, not only by its sha** — a release
  moves a tag over a rebuilt `dist/`, which the sparse fetch does not take, so the commit is new
  and the database is byte-identical. `abom_commit_content` records a sha256 over every file the
  fetch produced, and `QueryRunExistsForContent` matches every part of the run identity *except*
  the commit, joined on that digest. Measured against the corpus itself — the **480** commits ABOM
  has actually built databases for across the 30 repositories with more than one — **427** are
  distinct trees, so **53 rebuilds (11.0%)** were redundant, each one a database build plus both
  query lanes. `actions/cache` is 15 commits over 9 trees, `actions/checkout` 36 over 27. Fourteen
  of the thirty gain nothing, which is the honest shape. Over release *tags* rather than analyzed
  commits the share runs higher — **16 of 60** across ten popular actions, with
  `github/codeql-action` v4.37.7/.8/.9 sharing one tree. Nothing is written for the new commit:
  copying the ledger rows forward would record a run that did not happen, and the run stays
  attributed where it occurred. `.git` is excluded from the digest because its packfiles differ
  between two clones of one commit, which would have made every digest unique and the check inert
  — silently, since an inert check looks exactly like a correct one that never matches. The
  Discovery *suites* column had to move with it: it joined a row to runs at its own `resolved_sha`
  and a row settled by equivalence has none, so it would have answered *not recorded* for a
  repository that is analyzed — inventing a coverage gap out of an optimization, the mirror of
  claiming coverage never earned. Both content rows in that join are constrained to the same
  repository, so an identical tree in a fork still joins to nothing.

- **A reference is resolved before it is cloned, and probed against itself** — the skip already
  refused an unchanged query against an unchanged identity, but the checkout in front of that
  decision was still paid, on the reasoning that the commit is only knowable once the source is in
  hand. A reference resolves remotely for a fraction of a clone, so it is knowable first. Measured
  on the real corpus over the same twelve rows, requeued after being analyzed: **12 repositories
  fetched in 11.25 s becomes 0 fetched in 4.48 s**, with both settling twelve items and ingesting
  nothing — identical outcome, no clones. **803 of 2,800** settled rows are SHA-pinned and are not
  probed at all, since a reference that is already a commit cannot move. Failure is never the
  answer "nothing outstanding": an unanswerable probe falls back to fetching, and so does a
  reference mistaken for a commit. Generalizing the resolver surfaced a **latent defect in the
  drift recheck**: it resolved HEAD for every candidate while `Recheck.Requested` sat unused
  beside it, so a row asking about `v4` was compared against the default branch — an unmoved tag
  reads as drifted, re-analyzes to the identical result, and is due again next pass. Checking why
  the corpus had not paid for it was worth more than the fix: `checked_at` is set by `Complete` at
  analysis time and never meant "probed", and the honest signal `checked_at > analyzed_at` is
  **0 rows across all 2,800** — the recheck is wired but gated on a drained queue plus a 24-hour
  TTL and has never run here. It would have put **800 of the 1,997 mutable rows** on a permanent
  24-hour treadmill. One assertion was rewritten rather than deleted: the test that demanded a
  fetch now demands the recorded commit, which is the guarantee it was always for.
  `db/analysis-ledger.md`

- **`internal/archive` is deleted, and dead packages are now asserted against** — a complete,
  tested pipeline that `go list -deps` showed **no command reached**, and nothing outside it
  imported. The entry offered a command or deletion; adopting it would have reintroduced the
  defect the ledger exists to prevent. `consumeFindings` recorded a query run with **no pack, no
  digest and no CLI version**, so every row would have joined the **1,555 pre-identity rows** that
  can never converge, and it passed no `CLIVersion` to `FindingsFromSARIF`, skipping the
  `semanticVersion` check that stops SARIF being attributed to an analyzer that did not produce
  it. Its query lane also still called `RunQuery` per query, the pattern removed everywhere else
  that morning. Nothing was lost: "run our queries over archives already on disk" is what
  `pipeline.DatabaseSource` is documented to extend, so it is an adapter rather than a parallel
  `Runner` with its own pool. **The guard matters more than the deletion** —
  `TestEveryInternalPackageIsReachableFromABinary` was written first, failed naming exactly one
  package, and passes now. Reading dead code answers "does this work" and never "does this run".
  **The measurement for removing unreachable code is that nothing moves, and nothing did**: a real
  round afterwards running both lanes left findings at **11,370** and edges at **46,264**, with
  the ledger rows it wrote carrying full identity beside an older pre-identity row. The same
  hazard one level down is recorded rather than folded in: `ingest.FindingsFromDirectory`
  now has no caller outside its own tests, and a package-level guard cannot see it.
  `api/query-suites.md`

- **The BQRS lane costs one process, then none** — `query run` per query became one
  `database run-queries` for the lane, and then `runner.QueryServer` moved the evaluation into a
  `codeql execute query-server2` session that outlives the repository. One repository's three
  graph queries: **11.76 s → 4.34 s** for the batch, of which **1.47 s** is evaluation and
  **0.72 s** is the process-and-JVM floor. The entry priced this at "3 x 0.7 s a repository" and
  was **wrong by about five times**: the JVM is a fifth of what each process wastes, the rest
  being library-path resolution, pack resolution, opening the dataset and evaluator teardown —
  all per *process*, which is why one invocation recovers most of it and a session recovers the
  rest. Serialized time, since `runner.Lock` holds CodeQL to one invocation machine-wide, and
  three lock acquisitions a repository become one. Results are byte-equivalent across all three
  routes: **43 / 84 / 7 tuples**. The protocol is the CLI's IDE plumbing and is not a stable
  interface: parameters go **inside a `body` envelope** beside a `progressId`, and sending them
  bare answers `Internal error` with a NullPointerException that says nothing about the shape —
  unreachable by any unit test, which is why `make itest` now builds a database and runs the
  protocol against a real server. Every failure degrades to `ErrServerUnavailable` and re-runs as
  its own process, while a verdict the evaluator returns does not, because a query it rejected
  would be rejected again. **The fork this opened is refused, and the corpus said why:**
  `database analyze` does split into `run-queries` plus `interpret-results` with CodeQL still
  assembling all 35 rules, but `interpret-results` finds results by pack-relative path, and given
  a layout it does not recognize it **exits 0, says "Exported results to SARIF", and writes zero
  rules and zero results** — every repository clean, the crawl healthy. `FindingsFromSARIF` now
  refuses a run that names no rules at all. `db/analysis-ledger.md`

- **The selection controls reached the interface** — requeue by staleness and by repository had
  existed for `expand-all` since the ledger landed, and the Discovery page offered whole-tier
  requeue and nothing else. Both are now buttons, and the selecting moved into
  `expand.RequeueStale`/`RequeueRepository` so the page and the command line cannot drift into two
  answers to "what is out of date". On the real corpus, against **2,800** settled dependencies:
  every tier puts back **2,800**, out-of-date puts back **987** against CodeQL 2.26.4 and 2 packs,
  and one repository puts back **218 with 221 moved ahead of the ranking**. **The plan's own
  "that is a small saving today" is now false** — it was measured when 1,555 of 1,571 ledger rows
  predated the identity and only 7 items could be excluded; with 3,917 of 5,472 rows now carrying
  one it excludes **1,813 of 2,800**, two thirds of the clones a tier sweep pays for. Nothing about
  the mechanism changed, only what there was to select on. The corpus then produced a state nobody
  had written: asking for a repository whose rows a stale sweep had *already* queued reported
  `0 requeued`, correctly, and the page said *nothing analyzed for it* — wrong twice over, because
  the repository is known and "run this next" could still be answered. Expediting now runs whether
  or not anything was put back. Separately, a cycle that only skipped moved **no number the page
  shows**: `Processed` excludes skips by construction, so a re-analysis of a current corpus left
  `Analyzed` at zero and read as a hung loop, while the cycle log had said `skipped N already on
  record` all along. `Status.Skipped` carries it now. `db/analysis-ledger.md`

- **A dependency row names a version, not a package** — the queue is keyed `(node_key, ref)` and
  the grid drew bare names, so fourteen consecutive rows read `actions/checkout` and looked like
  duplicates. They were fourteen versions. Rows now read `actions/checkout@v4`, and an empty
  reference says **(default branch)** rather than reverting to the bare name — **1,253 of 2,352**
  rows have one, so a blank there would have re-created the bug for half the queue. Reconciling
  corrected the plan three times before any code changed: the C# `FrontierItem` had **no `Ref`
  property at all**, so the Go struct and the JSON carried it and the model dropped it silently;
  the "Analyzed commit" column already renders `ResolvedSha`, so the tag-and-resolution pairing
  mostly existed; and `.abom-ref` already existed as a styling class. `Note` became **Used by**,
  backed by the edges rather than the queue's `discovered_from`, which holds only the first
  repository to reach a dependency — that single name was what the old column printed.
  **The count is split by ownership, and the split is the whole point:** `actions/checkout@v4` is
  named by **220 repositories, 7 of them yours**; counting them together reads as exposure the
  operator owns, the same conflation that once inflated blast radius 202 → 209. It reuses the
  `abom_account` predicate behind `isOwned` so ownership cannot drift from what the owned tier
  means, and it never prints "0 yours" — with no verified account that is an unanswered question,
  not a count of none. The corpus check then found the interface exposing real corruption: the
  junk `/` repository appeared in **27** rows' previews, now **0** after cleanup.
  `ui/discovery-references.md`
- **The queue stopped settling anything, and filed the results under "/"** — found by pausing a
  crawl and watching "Finishing 40" never finish. `byRepo` is keyed `owner/repo@ref` but `prepare`
  stamped the prepared job with `owner/repo`, so `Consume` missed the map and Go handed it a
  **zero-value batch**: no items to settle, and `full()` on it is `"/"`. Analysis ran, was filed
  under a repository literally named `/` (**125 query runs, 203 edges**), and **40 rows sat
  `running` with `attempts = 0`** while `done` froze at 1,169. It only bit once references existed,
  because `key()` and `full()` are the same string without one — which is why the cycle before it
  settled 20 and every cycle after settled 0. A map miss is now an error rather than a zero value.
  Fixing it surfaced a second one: `settle` hardcoded `Resolution{Ref: "HEAD"}`, and `Mutable()`
  decides drift by comparing that to the commit, so **every SHA-pinned row was recorded as movable
  and would be rechecked forever**. Both were regressions from the per-reference work earlier the
  same day; a drained queue would have looked fine. `db/analysis-identity.md`
- **A row says what analyzed it, or says it does not know** — the Discovery page configured which
  suites run on which tier and then listed rows with no way to tell what any of them was analyzed
  with, so a row analyzed by the graph pack alone — no findings because **nothing looked for any**
  — was drawn identically to a row that was searched and came back clean. The entry framed the gap
  as "408 of 1,707 runs predate `db_commit`"; measuring from the page instead of the run table
  found it four times worse, because carrying a commit is not carrying a pack: **1,555 of 1,707
  runs record no pack at all**, and only **50 of 1,149** analyzed rows can name their suites. So
  the column ships answering *not recorded* for **1,099** rows, which is the honest result — pack
  identity began being written part-way through this corpus, cleanly (nothing before 15:50 has it,
  everything after 20:01 does), so it fills in as re-analysis proceeds. Attribution never
  derivation: the suites come from the run recorded against the row's commit, never the tier's
  current binding, which would make a suite enabled this morning look retroactively applied. The
  flagged set was checked against the same set computed independently in SQL and **matched
  exactly, with no false positives**. Two guards are load-bearing and were tests first: without
  the empty-string checks the join degenerates into every commitless run matching every
  unanalyzed row, so a *queued* dependency would have reported suites it never saw. And the read
  path had no index — `SCAN r`, twice per row, **198 ms against 9.7 ms** — which meant narrowing
  `TestIndexesAreNotCreatedByMigrate` from a categorical ban to the bulk-loaded tables its own
  reasoning names, because a read-only server never calls `CreateIndexes` and would never have
  had it. **Re-measured an hour later mid-crawl, with no code changed: 105 rows now name their
  suites**, so the gap closes by itself and needs no backfill. `db/analysis-identity.md`
- **The Explorer draws a version, not a name** — the queue analyzes per `name@ref` but the graph
  still drew one node per dependency, so the coverage the previous entry exposed had nowhere to
  land: a node was one dot whatever it was used at. Nodes now split on the reference the queue
  actually fetches, each carrying its own state, depth, score and dependents: **9,811 → 12,073
  nodes, 2,650 of them per-reference**. The predicate is what took the work: splitting every
  external action stranded a reusable workflow inside a seed repository, which is analyzed with
  its repository rather than per reference, and the exposure scope caught it as an edge whose end
  had left the graph. Splitting only what the *queue* fetches per reference fixed all four
  failures at once. The corpus then disagreed with the unit tests twice more — **700 dangling
  edges**, all composite actions we had in fact read but whose analyzed version no edge
  referenced, then **16** with outgoing edges and no analyzed version at all. A version we
  analyzed is a version that exists, so the first are emitted; the second cannot be placed
  without inventing one, so those stay whole. **0 dangling edges** at the end. The label bug
  reported alongside it was not a collision: force-graph's built-in tooltip and the view's own
  canvas labels were both drawing every node, at different positions with different text, and the
  tooltip kept naming the last node it saw once anything was selected because it knows nothing
  about our selection. One labelling system now, and it carries the reach the tooltip used to
  add. `ui/explorer-references.md`
- **The Explorer counts references, not just names** — the queue analyzes per reference while the
  graph draws one node per dependency, so `analyzed` was true as soon as *one* reference had
  results. On the real corpus that meant `actions/checkout` reported analyzed at **1 of 27**, and
  the "not analyzed" filter — `if (f.unanalyzedOnly && n.analyzed) continue;` — excluded every
  partially covered action. Nodes now carry `refsInUse`/`refsAnalyzed` through the full graph, the
  delta metrics and the node detail, and the filter asks `fullyAnalyzed()`: **"not analyzed" goes
  from 8,662 to 9,129 nodes, the 467 difference being exactly the partially covered set** counted
  independently in SQL before any code changed. The node page states "1 of 27 references analyzed"
  where it used to say "yes". Two places would have dropped the counts without failing anything:
  `mergeInto` overwrites metric fields when a delta arrives, and `collapseGroups` folds nodes into
  groups — a group summing nothing would have re-hidden what the change exposes. `analyzed` itself
  was left alone: it answers "is anything downstream known", which is a real boolean question, and
  redefining it would have changed a field several callers already read correctly.
  `ui/explorer-references.md`
- **Analysis identity: the queue analyzes references, not names** — everything was analyzed at the
  default branch (**1,213 of 1,213** rows at HEAD) while the organization runs many versions of the
  same action, so a repository pinning `v3` was reported from a reading of `main`. The frontier is
  re-keyed `(node_key, ref)`, checkouts fetch the reference asked for, and coverage is now
  measurable: **3,298 `name@ref` pairs in use, 574 analyzed (17%), 30 queued, 2,668 never queued**.
  The 574 are exactly the pairs whose `uses:` names no reference — the only ones a default-branch
  reading legitimately covers — against an interface that said 1,149 analyzed and looked complete.
  The rebuild is verified on the real corpus: **1,253 rows in, 1,253 out**, `done` and
  `resolved_sha` unchanged, existing rows keeping `ref = ''` rather than an invented `HEAD`. Three
  things would have been silently wrong: every `WHERE node_key = ?` in the queue would have
  completed all references at once, now impossible because `frontier.Key` is the only way to
  address a row; `groupByRepository` would have analyzed one tree and attributed it to every
  reference; and the Explorer's per-node state came from a **scalar subquery, which returns an
  arbitrary row rather than an error** — it now aggregates so the least settled reference wins, a
  node with one queued reference being not analyzed rather than analyzed. Also carries
  `analyzed_at` to the grid (**1,149 of 1,149** populated, reaching no Go struct), tier size beside
  tier analyzed (headers said 9 / 458 / 682; the tiers **hold 16 / 518 / 719**), and replaces the
  `Depth` column with the tier — owned dependencies sit at depths 1, 2 and 3, so **7 of 16** the
  operator can change themselves were drawn as somebody else's intermediary. `db/analysis-identity.md`
- **Job ordering from `needs:`** — the graph had only *uses* edges and no *after* edge at all, so
  "what stands between the entry point and this step" had no answer. `AbomEdges.ql` now emits
  `job-needs-job`: **331 edges across 54 re-analyzed repositories**, 35,457 → 35,803 edges and
  54,509 → 54,527 nodes, the new nodes being jobs no `uses:` ever names. **The aggregates held
  still, which is the result**: blast radius rows 2,194 → 2,194, `SUM(dependent_repos)` 3,127 →
  3,127, runtime/build-time 578/675 unchanged, and no job anywhere holds a blast radius. The
  proof is the one number that moved: `SUM(direct_dependents)` equalled the edge count exactly
  before, and afterwards reads **35,472 = 35,803 minus exactly the 331 ordering edges**. Two
  things nearly went wrong. Blast radius read every edge unfiltered, so ordering would have
  counted as use — both guard tests failed before the filter, which now lives in one place as
  `store.DependencyEdgeFilter` so the aggregates cannot disagree about what a dependency is. And
  the first corpus reading showed "unchanged" while `abom_graph_generation` sat at the old
  watermark, which is an absent measurement rather than a zero; only the rewritten sums settled
  it. `api/reachability.md`
- **Theme and layout audit** — the graph palette was measured to two decimals while MudBlazor's,
  which every chip, alert and button reads from, was not measured at all. Light `Success`
  **3.45:1** and light `Warning` **3.58:1** against white, both under the 4.5:1 text bar, because
  an *outlined chip draws its label in the semantic colour* — and this interface uses exactly that
  for "build-time only" and every pinning level. Now `#2A833B` (4.76:1) and `#C25100` (4.70:1);
  dark was 6.24–8.59:1 throughout, which is the recurring lesson that the scheme people look at is
  the one already correct. Both constants double as light discovery states, so the change had to
  clear the chroma and hue floors: `retrying` vs `unavailable` improves **21° → 25°** against a 20°
  floor it had been sitting one degree above. The coverage strip's orphaned sixth item was not a
  width problem — the items want **1,062px against 880px** at 1152, so no gap change could seat
  six on one line — it was a *column count* problem, and `auto-fit` picks five for six items. Now
  stated as 2, 3 or 6, the only counts that divide six: **5+1 → 6 across at 1600 (70px → 32px)**
  and 3+3 below it, nothing clipped at any width. `ui/theme.md`
- **An Owned tier, a hidden self-check, and a queue colour that is a colour** — `owned` sits ahead
  of `direct` and is keyed on verified accounts rather than depth, because a dependency you own is
  reached wherever the graph places it. On the real corpus the split moves **448 / 689 → 16 / 439
  / 682**, summing to the 1,137 done exactly, so nothing is counted twice; migration seeded
  `owned` with both of `direct`'s suites and left the others alone, so the first round runs what it
  ran before. Self-check is excluded by **pack**, not by suite name — which is what the earlier
  "filtering it out is the wrong fix" note was protecting, and the pack restructure is what made it
  possible, since the suite now sits alone in a pack that exists only for CI. `Queued` went from a
  **9%-saturated** slate to a real yellow with `Retrying` boldened to keep the pair apart, behind a
  chroma floor and a hue-distance floor that the previous guard could not express: it asserted
  "not identical to Muted", which a second grey passed. The hue floor could not be set where it was
  first written — 25° is unsatisfiable when three of five states share a 60°-wide warm arc, so it
  is 20°. The third tier also silently regressed the Discovery header — the box went **150 → 188px**
  and the queue start 441 → 519 — because `flex-wrap` breaks lines from content widths *before* it
  shrinks anything, so the sentence beside the controls could not give the width back. Fixed by
  denying the row the ability to wrap above 1100px. `ui/tier-suites.md`, `ui/discovery-config.md`

- **The skip is per lane, not per repository** — `pendingWork` asks the ledger about each lane
  separately and `prepare` runs only what is outstanding. The deferral that preceded this was
  justified by "the database build dominates", which measurement refuted: the build is **13%** of
  a repository and the two lanes are **87%**. On the real corpus, editing one line of
  `AbomEdges.qll` and re-running 10 repositories already on record produced **30 graph queries and
  0 findings analyses** — the four analyses in that pass were all first-time repositories, checked
  individually rather than assumed. Before, all 10 would have re-run the 35-query analyze, about
  **118 s of ~250 s** spent on queries whose digest had not moved. `db/analysis-ledger.md`

- **The small commands stopped paying for a JVM** — `runner.CLIServer` keeps one `codeql execute
  cli-server` alive and routes `resolve metadata`, `resolve queries` and `bqrs decode` through it:
  **0.75 s → 0.004 s** and **0.62 s → 0.004 s** per call, about 190x. On a 15-repository crawl that
  is 45 decode process starts eliminated plus 38 metadata starts a round, and 60 samples during a
  live crawl saw **zero** such subprocesses. The entry's "2-5 s of JVM start" was wrong — it is
  about 0.7 s — but the payoff is bigger than framed, because the decode is paid per query per
  repository. The design turns on something nothing predicted: **a command the CLI rejects kills
  the server**, empty answer and exit 2, so a bad command and a dead JVM are indistinguishable
  on this protocol. Every failure therefore drops the process and re-runs as its own, which is
  safe only because everything routed there is read-only. `query-server2` is split out and not
  done. `db/analysis-ledger.md`

- **Re-processing is proportional to what moved** — `RequeueStale` puts back only the items whose
  repository was not analyzed at the current identity, `RequeueRepository` aims at one repository,
  and `expedited_at` is honored ahead of `score` in `Claim` and in the queue view. Both reachable
  as `expand-all --requeue-stale` and `--requeue owner/name`. On the real corpus: a whole tier is
  **1,117** items, `--requeue-stale` is **1,110**, and after editing one `.qll` it is **1,117**
  again — the pack digest moving is what makes the previously current repositories stale. A
  repository scoring **0.0** was analyzed first, ahead of **93** pending items topping out at
  **2.0**. The identity comes from a fresh suite resolution rather than the store, because after a
  CodeQL upgrade the recorded version is the old one and selecting against it would call the
  corpus current exactly when all of it went stale. Not named `pinned_at` as the plan asked: a
  pinned reference already means a SHA-pinned uses: clause everywhere else here.
  `db/analysis-ledger.md`

- **Every finding says what it is, independently of the run** — `normalize.ResultHash` over the
  rule, the repository-relative path and CodeQL's `primaryLocationLineHash`, stored on
  `abom_finding.result_hash`; and `tool.driver.semanticVersion` checked against the `cli_version`
  the ledger recorded, so SARIF from a different analyzer fails ingestion rather than being
  attributed to the wrong one. The plan's own "only real test of verifiable" passes on the real
  corpus: one repository analyzed, its ledger rows and findings dropped so the skip could not
  fire, analyzed again — **15 findings, byte-identical hashes across both rebuilds**, and **zero**
  version mismatches. Grouping across the corpus is **115 groups over 307 rows, widest 10
  repositories**; the plan had guessed twelve. Reconciling also caught the chain contradicting
  itself — it specified `result = H(analysis, normalized result)`, and an analysis hash contains
  the repository, so that hash could never have been shared across repositories at all. The third
  part of the entry, `NOT NULL` identity columns, is **not done and should not be**: 1,555 of
  1,571 ledger rows still predate the identity. `db/analysis-ledger.md`

- **Work already on record is not done twice** — `expand.alreadyRecorded` asks `QueryRunExists`
  for every row `tierPlan.runs` would write, and a repository with all of them present is settled
  without a database build, a query or an analysis. Four repositories re-analyzed immediately
  after analysis: **109 s → 28 s wall clock, 20 → 0 CodeQL invocations, 16 → 0 ledger rows**, with
  nodes, edges and findings unchanged. The skip sits after the checkout because the run key
  includes the commit, and deciding from `abom_frontier.resolved_sha` would skip precisely when a
  ref had moved. It travels as `Prepared.AlreadyRecorded` rather than `ErrSkip` because only
  `Consume` completes items, so a skip returned as an error strands its batch forever. Two callers
  outside the expansion read "nothing analyzed" as "nothing left to do": `expand-all` would have
  ended its round loop on the first fully skipped round, and the supervisor would have logged
  nothing for the duration of a re-analysis, which is indistinguishable from a hung crawl.
  `db/analysis-ledger.md`

- **Every run says what produced it** — `abom_query_run` carries `pack`, `pack_version`,
  `pack_digest` and `cli_version`, all inside `run_key`; `runner.PackDigest` hashes the pack
  directory and `DigestCache` keeps one hash per pack per process, since the 35-query findings
  suite would otherwise re-walk `actions-queries` once per repository. On the real corpus the
  three graph queries record `abom-actions-graph-extraction-src@2.26.4`, the findings lane records
  `codeql/actions-queries@0.6.34`, and every row carries `cli_version 2.26.4`. Two defects that
  unit tests could not see: `pack_digest` was added to `CREATE TABLE` but not to `addedColumns`,
  so a real database — upgraded in place, being hours of crawling — failed at the first write
  while every fresh-database test passed; and `plansByTier` merged suites by appending the two
  query slices only, dropping the round's CodeQL version on the way into the tier, which showed
  up as a NULL `cli_version` beside a `pack` and digest that looked right, because those travel
  inside `plannedQuery`. `db/analysis-ledger.md`

- **One CodeQL invocation at a time** — `runner.Lock` around `database create`, `database
  analyze` and `query run`: a mutex for this process's workers and an advisory `flock` for a
  second abom process, cancellable so pausing discovery need not wait out a database build.
  Measured at **1 concurrent invocation during a `--concurrency 4` crawl**, against 4 by
  construction before. The measurement was wrong twice first, which is why the corrected sampler
  is checked in: `pgrep -f` matches the sampling shell's own argv, and one invocation is a chain
  of three processes — the `gh` shim, the `codeql` wrapper, the JVM — all carrying the same argv.
  Counting lines reports 3 for a single run. `db/analysis-ledger.md`
- **One Discovery control box, measured** — run controls, re-analyze and the suite binding in one
  box; a suite line carrying its pack version and query count, expanding to the query ids behind
  its own disclosure. At a 720px viewport the box is 150px at 1600 wide and the queue starts at
  441 with 7 rows visible, against 517 for the old two-panel layout; at 1280 the re-analyze
  buttons wrap and it is a wash. Consolidating alone *cost* height — 24px to a 1px divider's
  margins and 18px to uppercase labels wrapping — which is why the entry existed. And
  `white-space: nowrap` made it worse before it made it better: the row overflowed instead of
  wrapping, clipping "Re-analyze direct (42", a truncated number on the control whose job is
  stating cost. Nine component tests stayed green through it. `ui/discovery-config.md`
- **Packs are the versioned unit** — `ql/` restructured to mirror
  `server/ql/<lang>/<purpose>/{src,test}`: `abom-actions-graph-extraction-{src,test}`,
  `abom-actions-findings-src`, `abom-actions-selfcheck-src`, all scoped under
  `advanced-security` and all at `2.26.4`, matching `.codeql-version`. Two of the four own no
  queries, which is the point: their version tracks *which upstream queries ABOM selected*.
  Named `graph-extraction` rather than `extraction`, which in CodeQL already means turning source
  into a database — the opposite end of the pipeline from these queries.
  Suites are now `<pack>:codeql-suites/<name>.qls` everywhere, an identity that survives both the
  directory move and publication, and `AvailableSuites` reads pack manifests rather than one
  directory. Stored bindings are rewritten by a migration that runs before seeding, so a
  deliberate choice is not overwritten and an unrecognized binding is kept verbatim.
  `codeql resolve queries` returns 3 / 35 / 28 — unchanged by the move, which is the whole test
  of a rename. `ql/packs.md`
- **`make check-versions`** — exact pinning writes the same upstream version into a suite, a pack
  manifest and a lock file, and `codeql pack upgrade` rewrites two of the three, never the one
  inside a `.qls`. Proved by injecting a wrong pin rather than by reading the code — which is how
  the first version was caught running its loop behind a pipe, in a subshell, reporting every
  mismatch and still exiting 0. `ql/packs.md`
- **Discovery configuration folds; what it is set to does not** — the suite control went from
  304px to 62px and the frontier table from 666px down a 720px viewport to 517, so the queue is
  readable without scrolling. The header keeps stating the binding — `direct:
  abom-dependency-graph, abom-findings · transitive: abom-dependency-graph` — because a fold that
  hid it would leave a reader assuming the default. Two departures from the plan: the
  expansion-panel trap it warned about was *avoided* rather than handled, since rendering the body
  under a plain `@if` makes a folded grid genuinely absent and therefore assertable; and nine
  green component tests said nothing about the header rendering centered, which one screenshot
  caught at once. `ui/discovery-config.md`
- **One dev server per machine, enforced by process** — `start`, `stop` and `status` all find the
  server the same way, by executable name plus a `serve` argument, instead of trusting a pid file
  that only remembers what the Makefile itself launched. A server started by hand used to be
  invisible to all three: `start` reported success because the *old* server answered its readiness
  probe, `restart` was a no-op, and `status` denied it existed — which is how an afternoon of
  changes stayed invisible behind a binary seven hours old. The guard is the process, not the
  address, because a second server on another port is still a second writer against the database.
- **The tier binding control says why, where the choice is made** — the rationale moved out of a
  paragraph above the grid, which named neither tier, into the column headers: each tier now
  carries its name, why it differs from the other, and the work a tick would apply to. A read that
  *failed* is no longer drawn as a read still arriving — an indeterminate bar spanning the panel
  read as a pulsing border and waited for ever against a server too old to have the endpoint.
  `ui/tier-suites.md`
- **Findings lane for upstream repositories** — a crawl now runs what its tier is bound to in both
  lanes: findings-shaped queries through `database analyze`, everything else through `query run`,
  from one resolved plan. The reconciliation was the whole story — `AnalyzeDatabase`,
  `FindingsFromSARIF` and `internal/archive` wiring them together all existed and were tested; the
  gap was one `continue` in `expand.plan`, and `internal/archive` turns out to be unreachable from
  the binary. Two things were wrong on the way: the findings run was given an invented query id
  when every result already carries its rule, and the edges run was named from `--query-id`, which
  is empty once a crawl runs from a binding. A binding with no analyzer is now refused rather than
  absorbed. On the reference corpus a single round took findings 4,531 → 4,620: 89 findings across
  18 upstream repositories under 4 rules, none of which a crawl had ever produced before.
  `api/query-suites.md`
- **Per-tier suite controls** — a grid on the Discovery page, one row per suite and one column per
  tier, each column headed by the work a change would apply to (direct 380, transitive 691 on the
  reference corpus). Suites are read from disk per request, so one appears by being dropped in;
  anything already bound stays offered even when it is not on disk, because a binding may name a
  pack's suite. The corpus check argued *against* the obvious tidy-up: filtering out
  `abom-selfcheck.qls` by name would reintroduce exactly the release-coupled hard-coding the
  control exists to remove. **Since superseded, and by the ground moving rather than by the
  argument losing:** the pack restructure put that suite alone in a pack that exists only for CI,
  so the exclusion became a property of one pack instead of a list of suite names, and a new suite
  in an analysis pack still needs no release. `ui/tier-suites.md`
- **Tier → suite binding as configuration** — `abom_tier_suite`, seeded at migration and
  rewritable at run time; a batch is planned by the shallowest depth it holds and a shared suite
  resolves once per round. On the reference corpus: direct binds graph + findings, transitive
  binds graph alone, and `PUT` edits either without a restart. The design needed a second table
  it did not start with — "bound to nothing" is a real instruction and was indistinguishable from
  "never configured", so every restart would have seeded the defaults back over it.
  `api/query-suites.md`
- **A scoped Explorer that is legible** — the default is `scope=exposure`: only the routes by
  which repositories under review reach an action an advisory names. 277 nodes and 486 edges,
  14 KB on the wire against 473 KB, 45 MB of heap against 419, at 60.7 frames per second, and
  44 ms of server time against 331. Folding bought frames; scope bought meaning — at 277 unfolded
  nodes an edge can be followed end to end, which 603 owner groups never allowed.
  `ui/performance.md`
- **A palette checked as numbers** — "Queued" was the same string as `Muted`, the colour for a
  node that was never a dependency, at 2.69:1 on dark and 2.07:1 on light against a 3:1 floor, so
  a queue of 422 read as a disabled control. Auditing the whole palette found three more: `org`
  and, worse, **`Focus` — the selection colour — at 2.48:1 in light mode**, so selecting a node
  barely changed it. `PaletteContrastTests` now computes WCAG contrast for every entry in both
  schemes, so this cannot regress by eye. `ui/theme.md`
- **An Explorer that can be used** — the simulation now holds only what is visible, the view
  arrives folded by owner, and a settled layout with a still camera stops drawing altogether.
  3.4 → 60.7 frames per second, 9,811 → 603 simulated nodes, 419 → 68 MB, and a heap that falls
  back after a filter instead of climbing to 643 MB. Three things the measurements killed:
  removing the simulation gate changed the default view by nothing, because with no filter every
  node is visible and the default was a scope problem all along; quiescence driven by the
  controls' `change` event never fired, because damping keeps that event coming for ever;
  and the heap was never a leak, it just tracked the simulation's size. `ui/performance.md`
- **Exact supply-chain classification** — the local "is this container referenced" test replaced
  by the seed-reachable closure. Runtime 832 → 578, build-time 421 → 675; 254 demoted, 0 promoted,
  none at depth 1. The old rule was not merely loose by one hop: it called `actions/checkout`
  build-time, and only `SeedFrontier`'s default plus `Enqueue`'s one-way stickiness hid it. Those
  same mechanics meant a tightened rule could never reach an existing corpus, so `Reclassify` is
  the one pass allowed to lower a claim. Cross-checked against `abom_blast_radius`, derived
  separately: afterwards zero disagreements, where before **254 rows claimed runtime while blast
  radius said no repository under review reached them**. `db/supply-chain.md`
- **Reach rendered, and conditions joined to it** — `/-/reach` now carries the `if:` guarding each
  step on a route, and the node page renders route and guard together. 5 of 23 repositories reach
  `aws-actions/configure-aws-credentials` only on nightly builds. Two things the corpus check
  caught: composite-action step conditions were keyed to a workflow path that names no node and
  collapsed to one row per file, and nothing captured conditions on a normal crawl at all — all
  67 that existed sat in upstream CI, which is never seed-reachable, so the join was correct and
  necessarily empty. Under the full suite: 67 → 620 conditions, 25 → 289 joined.
  `api/reachability.md`
- **`/implement-abom-plan`** — one command from "what next" to a measured before/after, with the
  two steps that get skipped by hand (reconcile first, diff the corpus last) made explicit.
  Unproven until it has driven an entry end to end. `tools/implement-plan-skill.md`
- **Advisory enrichment** — `internal/advisory` went from zero production callers to reachable
  from the binary. Advisories 20 → 34, `fetchedAt` unfrozen after a day, covered packages 12 → 23.
  The corpus check killed the original design: arrival alone is inert on a drained queue, and the
  refresh costs 109s for 936 packages, so it gained a 24h ceiling and a 15m floor.
  `api/advisories.md`
- **Transitive traversal** — classification from `abom_edge.edge_kind`, plus a drain-time sweep.
  415 never-queued dependencies → 1; frontier 573 → 987. `db/supply-chain.md`
- **Extensibility spine** — `abom_observation`, metadata-driven routing, four result shapes,
  `QueryRunExists`. A new query contributes facts with no Go. `db/observations.md`
- **Suite-driven execution** — `codeql resolve queries`, one round per resolve, findings left to
  their own lane. `api/query-suites.md`
- **Step reachability** — `ReachOfNode`, hops 0/1/n semantics, `/-/reach` and `/-/observations`.
  `api/reachability.md`
- **`AbomStepConditions.ql`** — `if:` verbatim, never evaluated. `api/reachability.md`
- **`AbomSurface.ql` resurrected** — was referenced by nothing but its own test; now the first
  customer of the observation path. `db/observations.md`
- **Tier re-analysis** — `RequeueTier`, guarded like Start, cost stated on the control.
  `ui/tier-suites.md`
- **bUnit component tests** — 10 tests in `make verify` and CI, each named after a shipped
  regression. `ui/testing.md`
- **Upstream `actions` PrintAST/PrintCFG fix** — `selectedSourceFiles`, matching the other nine
  languages. Previously returned nothing for any real repository. `api/reachability.md`

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
