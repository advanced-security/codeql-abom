# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the one-line
reason for it.

For what ABOM already does, read [`docs/architecture.md`](../docs/architecture.md). For where it is
going and why that is the destination, read [`docs/vision.md`](../docs/vision.md); for the same
thing in themes rather than tasks, [`docs/roadmap.md`](../docs/roadmap.md).

House rules:

- **Plans describe what is next, never what was done.** Git history is the record of what shipped.
  When a plan's last pending item lands, the plan file goes away; important updates to what the
  system now does move to `docs/*.md` first, and a rule that outlives the workstream moves to
  `.github/skills/**`. Conversely, `.github/instructions/**` is immutable from the agent perspective,
  where any exception to this immutability needs a loud prompt to the user with an explicit "ack".
- **A plan is as long as the work needs.** There is no line limit: a plan carries the measurement
  behind a claim, the alternative that was rejected and the query that produced a number, so the
  work can be planned and then implemented from the file alone. What it may not carry is narration
  of work already finished.
- **One entry is one piece of work, never a phase of one.** A plan with a cheap half and an
  expensive half is sequencing, and sequencing belongs inside the plan. Splitting it here spends two
  slots on one plan and pushes everything below it down twice.
- **Only rank what someone could start tomorrow.** Work deferred by decision, or blocked on
  something this workstation does not have, is listed once under *Deferred* with its reason. A
  deferred plan is still refined in place; that is what makes it ready when its blocker lifts.
- **Prefer work whose success can be shown end to end.** Every defect that reached the interface in
  recent memory was found by driving the real thing against real data, and none of them by a green
  build. `make e2e` is that, made repeatable.
- **A number in an entry names the data it was measured against.** Figures taken from the retired
  corpus have been removed rather than carried forward, because a remembered constant gets compared
  against instead of re-measured.

The ranking is itself a plan, and is subject to change.

## Ranked next

1. **[An edge carries what a reader wants to filter on](db/edge-facts.md)** — **unblocked, and back
   at the top**: a crawl of a real organization is running again, so the before/after diff every
   remaining item rests on can be taken. A `uses:` line *is* an
   edge, so *which references float on a tag*, *what crosses into another account* and *what sits on
   a route to a live advisory* are all questions about relationships, and none of them can be asked
   as one. No term filters `edge_kind`; `edge_key` is a stable key no response carries; `abom_edge`
   has one derived column and needs four. Now also carries the **relationships unit** of Inventory:
   actions, workflows, jobs, repositories, advisories and coverage all landed without it, and
   relationships are the one shape left, because a unit only exists once its columns are its own and
   an edge's columns are exactly what this plan stores. It is also where the `step`/`job` rows go:
   every `actions/unpinned-tag` finding sits on one, because a `uses:` line *is* a step. Storing the
   two relationships the graph draws and does not keep is part of it, and must move no aggregate.
   **Two of its eight items are done** — the browser already read the stored boundary, and a value
   outside a term's vocabulary is now refused rather than dropped.
2. **[One seeded corpus, shared](ui/testing.md)** — down from 1, deliberately, now that a real
   corpus exists again and no longer blocks the entry above. What is left here is a **different**
   corpus with a different job: `make corpus` builds a deterministic one offline from recorded
   CodeQL output — 3 repositories, 20 nodes, 9 edges, all four edge kinds, both reference styles,
   **a dependency between two distinct owned repositories** the crawled organization has never
   had, and 6 markers that all reach a node. It needs no CLI, no network and no token, which is
   what lets CI hold a populated database at all. It is not yet representative: pointed at it the
   browser suite runs **48 passed, 5 failed, 7 skipped**, every failure being the fixture rather
   than the product — no advisories, and too few rows for a column or a layout claim to mean
   anything. Until that run is green the e2e default stays the empty database, because a gate red
   for everyone teaches nobody anything. **The two corpora are not interchangeable**: the crawled
   one answers "did this change the real numbers", the fixture one answers "does this still work
   where nobody has crawled anything", and only the second can gate a pull request. **Entry 5 now
   depends on it**: a closure database's headline claim is that every existing total is unchanged,
   and only a deterministic corpus can hold that claim in CI.
3. **[A commit is fetched once, not once per reference](db/checkout-cache.md)** — `checkout.go`
   clones into a directory keyed by the **reference**, and what it fetches is a **commit**.
   `run_key` already hashes `db_commit` with no reference in it and `abom_ref_resolution` already
   maps one to the other, so the ledger and the resolution table both treat the commit as the
   identity and the fetch is the last step that does not. **132 of 225** dependencies are analyzed
   at more than one reference, and the tree is deleted after each unit, so every cycle re-clones
   what the last one threw away. Depends on nothing, pays immediately against the pipeline that
   exists, and is the precondition for entry 5.
4. **[An analysis says how its database was built](db/analysis-mode.md)** — cheap, and a hard
   prerequisite of entry 5 rather than a companion to it. Every database ABOM has built is one
   repository's own checkout, which is the shape Default Setup builds; a staged closure answers a
   different question with the same rule ids. Without `regular` versus `extended` on the row, reach,
   the bill of materials, the submission closure and the pinning counts all silently start mixing
   two meanings, and none of them would report that its inputs had changed. **The term is owed to
   the interface too** — a reader has to be able to tell *somebody will see this alert* from
   *nobody will ever alert on this, and it still reaches you*.
5. **[One database per owned repository, holding its whole closure](ql/closure-database.md)** — an
   investigation with a measured spike and no implementation, ranked here because the measurement
   it rests on is cheap and may stop it. `codeql/actions-all` resolves a `uses:` by string equality
   against a callee staged at `.github/actions/external/${owner}/${repo}/`, so the namespace is flat
   and **depth is free**: three chained staged actions resolve, and the upstream
   `actions/code-injection/critical` reports a **seven-step taint path** across all three on default
   filters. Four hazards were measured and are refused by construction, not patched — bare sibling
   staging resolved one local reference to **two** callables, `.github/reusable_workflows/` staging
   manufactured a taint path into a file the repository does not contain, a staged reusable workflow
   is invisible without a recursive index filter, and a tree with no workflow anywhere resolves
   nothing at all. **The ceiling is one reference per dependency per database**, which is survivable
   only because the closure is rooted at one owned repository. Its headline verification is that
   **every existing total is byte-identical afterwards**. Depends on 3 and 4.
6. **[What the extended analysis reveals, and whether any number should move](db/extended-findings.md)**
   — entry 5 is built to change nothing; this is where the decision to let it change something gets
   made, once there is something to look at. Reach is computed from `abom_edge`, so extended rows
   landing there without a decision would reorder the crawl, resize every node the Explorer draws
   and move half the remediation arithmetic **without anybody choosing it**. The deferral only stays
   open while its default changes nothing, which is the invariant this entry exists to hold. Its
   first item is looking at the data, and *no change* is a legitimate outcome. Depends on 5.
7. **[A unit is a node kind, and its columns are its own](ui/inventory-units.md)** — The rule holds for what a row
   says and not for what a reader can do with it. **Four actions sit above the unit strip, so all
   five units offer all four**: three bill-of-materials exports that ignore which unit they were
   pressed on, and *show these in the graph*, which hands the scope to a surface that draws nodes —
   and an advisory is not a node, nor is a repository. The bill of materials is also still a page
   that is neither a surface nor a unit, holding coverage and hop-depth claims no listing makes,
   reachable only by an address the navigation does not offer.
8. **[A term is enforced or refused, and today one is neither](api/filter-enforcement.md)** — it
   stopped being cosmetic. It still holds `abom_policy.scope_kind`, a second
   narrowing mechanism no reader can see. It now also holds the language's inability to say that a
   list is deliberately empty, so *every finding flag off* is a view nobody can share; and the
   question of which side applies each term, now that a third side is declared for listings. The
   divergence that was feared is not there — server and browser select **identical node sets** for
   `minReach`, `origins` and `ref` on the crawled organization — so what is owed is writing the
   split down, not repairing it. **A scalar term's value is now enforced**; `kinds`, `origins`,
   `edges` and `flags` are lists whose members are still unchecked, and whether partial validity in
   a list refuses or warns is this plan's decision. **Two more terms are arriving** — `mode` from
   entry 4 and `origin` from entry 13 — and each is declared on every side or it will drift.
9. **[Hiding something is a decision somebody signs](db/exclusions-and-exceptions.md)** — every
   organization carries things it has decided not to look at and things it has decided not to act on
   yet. `abom_policy_waiver` already models both, with a required reason, an author, and an expiry
   that is required for an exception and read when the decision is applied. It reaches nothing but a
   policy verdict. What is owed is a scope beyond one policy, an exclusion that actually stops work,
   an exception that hides a result while the work still runs and records, and a screen that refuses
   to write either without a reason and a review date.
10. **[A container image is not an account](db/container-images.md)** — `uses: docker://alpine:3.20`
    is parsed correctly, marked external and given no owner, and the graph then groups it by
    splitting its key on the first slash — which for `docker://` is the second character of `//`.
    Every container image in the corpus is drawn inside a boundary labelled `docker:`, an account
    nobody can be a member of and nobody can declare. The same fall-through once had ABOM reporting
    third-party dependencies on an account named `$`, so the fix is to refuse the branch rather than
    to correct its output. Cheap, visible, and startable against the corpus that exists.
11. **[Only one read still scales with the organization](api/org-enumeration.md)** — the listing is
    bounded by what moved and **the per-repository CodeQL probe is not**: there is no code-scanning
    field on `Repository` in the GraphQL schema, checked by introspection, so it is one REST request
    each. At twenty thousand repositories that is now the entire bill. The same entry owes the
    interface the state the API already carries — which route answered, when the account was last
    listed in full, when last swept.
12. **[GitHub already parsed the first hop, and we never compare](api/dependency-graph-crosscheck.md)**
    — our central claim is that the native Actions dependency graph stops at the first hop, and that
    claim is sourced from documentation rather than evidence. Nothing checks our CodeQL extraction
    against an independent reading of the same files either. `PackageURLs()` already reads GitHub's
    SBOM; it is pointed only at our own submissions. Ranked above the gate deliberately: a check run
    that blocks somebody's pull request should not be the first thing to discover the graph was
    wrong. Accuracy only — the seeding path is refused in the plan, because an SBOM describes a
    default branch and we analyze a pinned commit.
13. **[A query has an origin, and a finding remembers it](db/query-catalog.md)** — three kinds of
    query already run and nothing tells them apart: `abom_finding.provider` records the SARIF tool
    driver, which is `CodeQL` for upstream, ABOM and operator-written queries alike. The only
    available channel is the `abom/` prefix on a rule id — a **convention**, enforced by an
    instruction file rather than by the data, offered as though it were provenance. It also picks up
    the two columns that already exist and are **never written**, `query_version` and `pack_version`,
    without which *what has not run since pack X at vY* needs every suite re-resolved. Depends on 4.
14. **[A scope names a set of databases, not only a set of nodes](api/scope-to-databases.md)** — one
    predicate already means the same thing on three surfaces, and none of them can answer *which
    databases would have to be analyzed to settle this*, which is what every re-analysis and every
    simulation actually asks. `store.NodesMatching` returns nodes; getting to a run needs a
    repository, a commit and a database, and nothing assembles those from a `Spec`. **Does not wait
    on entry 5** — a regular per-repository database is a database, and the extended shape joins the
    same resolution later behind the `mode` term. Depends on 13.
15. **[Run these queries, against this scope, now](api/on-demand-analysis.md)** — the only way to
    run anything is to requeue a whole tier and wait for the crawl to reach it, which is the right
    shape for keeping an organization current and the wrong shape for every question a person
    arrives with. A crawl is not a feedback loop, and a feedback loop is what writing a query needs.
    Execution goes through the lanes `internal/expand` already has, because a second execution path
    would drift and the two would disagree about what a query means. Depends on 14.
16. **[A policy can only say what the filter language can say](api/policy-language.md)** — down to
    the half that matters: the language cannot say *carries a live advisory* or *is reached from
    something you own*, which is most of what an Actions rule is for. Both terms are unblocked and
    depend on nothing; each needs adding to the enforceable declaration in `internal/view` or the
    set path refuses it. **Above the gate deliberately, because the gate enforces whatever this
    says** — and the vision now names these terms as what a rule is about.
17. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM holds the
    only transitive Actions graph, so it is the only thing that can refuse a pull request on that
    basis. **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
    remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
    `store.EvaluatePolicy` rather than a second engine. The ground under it is firmer than it was:
    exposure and the submission closure now agree about which version a route belongs to, held by
    tests, so a verdict and a submission cannot disagree about the same supply chain. Depends on 16
    for what a rule may say. Its client is deferred in [`api/abom-check.md`](api/abom-check.md).
18. **[The purl names a version Dependabot can match](db/version-resolution.md)** — the submitted
    purl carries a version for most commit-pinned dependencies ABOM submits, with no request to
    GitHub. The open half is the direct commit-pinned entries that are not submitted at all, and
    closing that is not obviously safe. The measurement it was owed is now available rather than
    hypothetical: a crawled organization with **571 recorded ref resolutions** is on disk.
19. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — unblocked.
    The sweep was written against a corpus where nothing had ever been sighted, so it correctly
    condemned nothing and proved nothing. There are now **1,724 sighting rows** from a crawl that ran
    the findings suites, so the tombstone and the `_live` view can finally be exercised against a
    finding that really does stop being reported.
20. **[The same graph draws a different picture every time](ui/graph-layout.md)** — the force
    simulation has no fixed seed, so no two runs agree and nothing downstream can rest on a
    position: the screenshot layer and the browser suite's geometry claims are both looking at
    something different every time, which is why a test named *switching force preserves angle*
    exists at all. Graphviz clusters are non-overlapping by construction and report a bounding box,
    which is the ceiling `docs/roadmap.md` records — boundaries and relationships cannot be drawn
    at once. **Not a performance fix, and must not be sold as one**: removing every edge from the
    whole-corpus view still reaches 4.8 FPS, so the ceiling is the renderer. The encoder half is
    startable today; the layout half needs entry 2's corpus, because twenty nodes cannot carry a
    layout claim.
21. **[Remediation summarizes two things into their more reassuring halves](ui/remediation-honesty.md)**
    — down to one half. The strip-versus-table gap is closed: the advisories unit carries an
    `unreached` count and states it under the table, so **3 reaching you** and **8 reaching
    nothing** are both on screen and sum to the 11 the strip counts. What is left is `Fix:
    available`, one boolean over an advisory whose ranges include **no fix published**, with 5
    repositories sitting in exactly that range — a reader is still told the comfortable half.
22. **[Reach counts a repository that pins a version without the dependency](db/reach-version-constraint.md)**
    — exposure and the submission closure now agree about which version a route belongs to; reach
    carries none of it, and reach is what orders the crawl. **132 of 225** dependencies are analyzed
    at more than one reference. It sits below the interface entries because it is invisible to
    everyone and its first step is a decision that may legitimately conclude no change.
23. **[A backup is a command, not a thing everybody is told to remember](db/backup-and-restore.md)**
    — the correct incantation is written in the working-style instructions, printed by `make e2e`
    when it refuses the working database, and implemented nowhere. Getting it wrong raises no error:
    `cp` does not read the WAL, so the baseline is silently missing recent commits and a change that
    did nothing looks the same as a change that worked. The half that is a correctness fix rather
    than an ergonomic one is the epoch — a restore moves generation history backwards underneath
    every connected client, and nothing currently tells them to reload.
24. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate already holds new
    findings at zero in all four languages. What is left is complexity: **50 production functions
    over 10**, and `runServe` is the one that keeps tripping the limit when anything is added to it.
    Measured from source, so unaffected by the corpus retirement. Cleanup rather than risk
    reduction, which is why it sits here.
25. **[The coverage strip orphans a cell](ui/theme.md)** — with nothing declared the strip renders
    four items into a three-column grid, so the fourth sits alone on a second row of every surface
    that carries it: Inventory, the Explorer, Settings and the bill of materials. Only the 4 and 5
    item cases are affected; a populated corpus renders 6 and divides cleanly, which is why this is
    invisible in exactly the runs most likely to be looked at. Held by a `test.fail()` that will
    break the run when it is fixed.
26. **[A query is written from an example, not from QL](ql/query-authoring.md)** — the set of
    questions anybody can ask of the graph is fixed: whatever `codeql/actions-queries` ships plus
    one marker ABOM writes. Those are good general questions and they are not *this organization's*
    — which runner labels are permitted, which internal action must never sit beneath a privileged
    job. CodeQL expresses all of them; what stops a security team writing them is QL, the `actions`
    library and a fixture, and the person who knows the Actions patterns usually has none of the
    three. The [CodeQL development MCP server](https://github.com/advanced-security/codeql-development-mcp-server)
    already automates that loop and already enforces the rule this repository has been burned by
    breaking: **the fixture comes from the CLI at every step**. **Does not need entry 5.**
    Depends on 13.
27. **[A query's effect is known before anybody turns it on](api/query-simulation.md)** — a policy
    is simulated before it is adopted and a query is not, and the query is the more consequential
    of the two: publishing one changes what every developer in the organization sees, and its author
    is not who gets asked about the four hundred alerts it raised. **The prediction is sourced from
    `regular` mode and only from `regular` mode**, because Default Setup stages nothing and will
    never see what entry 5 reveals — which is what entry 4 exists for, arriving where it matters
    most. Exact for the commit analyzed, an inference beyond it, and it says which. Depends on 15
    and 26.
28. **[A query leaves ABOM as a published pack, never as an alert](ql/pack-promotion.md)** — the
    upload route is **refused rather than deferred**: it needed re-anchoring, stable fingerprints, a
    commit on every finding, a tool-and-category pair whose mishandling *deletes somebody's alerts*,
    and a confirmation gate in front of all of it. A configuration file applied through the
    `github-codeql-config-file` repository property is merged with what Default Setup generates, so
    the organization adds a pack through its own consent mechanism and ABOM writes nothing. **No
    alert exists that a person did not choose**, now satisfied by construction. Its first item —
    publishing ABOM's own four packs to GHCR — depends on nothing; the rest depends on 27.

**Everything above rests on a corpus, and there are now two of them** — a crawled organization for
before/after diffs, and a deterministic fixture one for CI. Entry 2 has stopped being a peer of the
entries below it and become a **hard dependency** of entry 5: a closure database cannot be held by a
gate without a fixture corpus to hold it against, and the crawled organization cannot gate a pull
request. Entries 1 and 7 to 9 remain one refactoring, split because they are four workstreams rather
than four phases of one: the units can be built against the edge facts that exist today, the edge
facts are worth having in the graph whether or not a grid renders them, the filter work is owed
to the gate whatever the interface does, and the governed exclusions are owed to the policy engine
regardless.

**Ten entries were inserted at once, which is a decision about everything below them, so it is said
plainly.** Entries 3 to 6 sit above long-standing interface and accuracy work because the checkout
cache pays immediately against the pipeline that exists, because the mode column has to precede the
first extended row or it cannot protect anything, and because the closure database's first item is a
measurement cheap enough that finding out it does not pay is worth more than the slot costs.
Entries 13 to 15 sit above the policy and gate work because a gate enforces rules, and the rules an
organization actually wants are ones nobody can write yet. **Entries 26 to 28 are ranked last and
are not blocked by entry 5** — a query authored against a regular per-repository database is the
useful default and is what Default Setup would run anyway; the extended shape widens what a query
can see and is not a precondition for asking one. The enumeration and crosscheck work beneath is no
less urgent than it was. Disagreeing with that is the point of writing it down.

**Investigate before refining.** Entries 5, 6 and 27 each open with a measurement rather than an
implementation, and each names an outcome where the honest answer stops the work: the closure may
collide on references more often than the shape survives, the extended rows may move no number worth
moving, and the prediction may not match what Default Setup reports. These plans are written to be
refined by what the investigation finds, not defended against it.

**Two of them came from looking at the running interface rather than from a green build**, as did
the interface-honesty entry that has just retired — which is the pattern rather than an anecdote.
`make verify` passed throughout the work that introduced every one of them, and passed again while
forty controls on four surfaces had no name a browser would announce.

**And measuring an entry is not the same as re-measuring it.** Interface honesty was written from
one audit and corrected twice on the way out: the accessible-name count was **40, not 56**, and one
of its five items — a second channel for node kind — had been built before it was written down. An
entry above may be carrying the same kind of error. Reconcile before starting, not after.

## Deferred, and why

Not ranked, because nobody can start them here. Each keeps its plan and is refined there; none is a
refusal.

- **[A client in the workflow, thin enough to trust](api/abom-check.md)** — deferred by decision
  rather than by obstacle, and refined in place until it is ready. It is the only component that
  would run in somebody else's repository under a token ABOM does not control, and a published
  action is pinned by its consumers, so a bad release outlives the fix.
- **[A coverage gap names its cause](api/ghas-enablement.md)** — needs a released v2 of
  `gh-ghas-audit`. The contract is its `schema_version`, never a branch, and pinning a version that
  does not exist yet means building against something that still moves.
- **[An address the organization can reach, and an identity it granted](api/org-server.md)** —
  ingress and an App identity, neither of which this workstation has.
- **[Read in response to events, not on a timer](api/event-driven-reading.md)** — the detection
  works; only the trigger is missing, and the address it would arrive at is
  [`api/org-server.md`](api/org-server.md).
- **[Findings are a pluggable class](db/finding-providers.md)** — the seam is built and carries real
  data; a second provider is deferred by decision.
- **[Every action has an API](api/api-first-invariant.md)** — already true, and not yet provable.
  Upkeep, with no client that needs it until the gate does.
- **[Collapse `name@ref` back to one node](ui/explorer-references.md)** — per-reference nodes are the
  useful default and the only behavior. The counts for the other reading already ride on every node.
- **[Does a transitive alert open a pull request?](api/dependency-submission.md)** — the last link in
  the remediation chain is an inference from GitHub's documentation, and `codeql-abom-test1` is where
  to measure it. Not to be softened in prose.
- **[A reference is known, or it is unknown in a way that can be named](ql/semantic-model.md)** —
  unblocked for design, and wider than it was: dataflow crosses a `uses:` boundary at any depth once
  the callee is staged, so what entry 5 gives it is real flow rather than a lone bare checkout. One
  bucket currently holds a literal matrix, an environment variable set two jobs earlier, and `uses:`
  built from `github.event`, which is an attacker choosing what runs. `abom_observation` already
  keeps both readings of a changed fact and already refuses to pick a winner between two, so the
  storage for this exists and the work is naming. Shaped so a second adapter — a Dockerized action's
  image, a JavaScript action's own dependencies, a shell script a composite action calls — is
  addable; none of those is in scope. Not yet ranked.
- **[Reach is computed one way, and nothing checks it](ql/reachability-crosscheck.md)** — blocked on
  entry 5. One in-memory walk produces reach, and reach orders the crawl, sizes every node drawn and
  is half the remediation arithmetic. Entry 12 is this argument one level down. A closure per owned
  repository is a better second derivation than one database for the account would have been: it is
  pinned at the references its root really uses, where a whole-account database would collapse
  **132 of 225** dependencies to one reference and then disagree for a reason about layout rather
  than about reach.
- **[A report somebody can hand to somebody else](ui/printable-report.md)** — blocked on entry 20. A
  drawing that is different every run cannot be printed, and the bill of materials is already half
  of this: a document that states its own coverage, with no way to carry the picture.

## Built, with refinements nobody has ranked

These **thirteen** plans describe workstreams that are **built and carrying real data**, and each
keeps a short list of refinements. They are not ranked because none of them is the next thing to
build; they are the backlog a workstream leaves behind, and a reader asking "what next" should not
have to read past them. They are listed because a plan in neither the ranking nor *Deferred* is
invisible, and all of them were, which is how an index stops being one.

Being unranked is not being unread. A refinement list is a fine place for polish and a poor place
for a failing verification criterion: one sitting here unread is one nobody is measuring against.

Promote from here when a refinement stops being polish — when it blocks something ranked, or when a
measurement makes it urgent.

| Plan | What its refinements are about |
| --- | --- |
| [api/advisories.md](api/advisories.md) | refresh cadence and what an age ceiling should cost |
| [api/query-suites.md](api/query-suites.md) | suite staleness, and streaming result sets out of BQRS; query identity moved to entry 13 |
| [api/reachability.md](api/reachability.md) | all-routes reach, and `needs:` ordering nothing renders |
| [db/analysis-ledger.md](db/analysis-ledger.md) | what a run records about the pack that produced it |
| [db/data-efficiency.md](db/data-efficiency.md) | the standing cost review; one endpoint is still linear in rows |
| [db/declaration-provenance.md](db/declaration-provenance.md) | how a declaration arrived, and what contradicts it |
| [db/freshness-budget.md](db/freshness-budget.md) | claim order, and depth 2 starting before depth 1 drains |
| [db/observations.md](db/observations.md) | the fact table's keys and its readers |
| [db/owned-scope.md](db/owned-scope.md) | ownership edges the declaration does not yet settle |
| [db/pruning.md](db/pruning.md) | tombstones, compaction and what a delta reports |
| [db/ref-provenance.md](db/ref-provenance.md) | requested versus analyzed refs through the ledger |
| [db/supply-chain.md](db/supply-chain.md) | classification over the seed-reachable closure |
| [ui/tier-suites.md](ui/tier-suites.md) | per-tier suite binding, its precedence, and the control entry 15 needs |

## Gaps with a decision to make

- **The expand lane cannot be told that interpretation changed.** `expand.pendingWork` decides from
  `store.QueryRunExists` — repository, commit, query id, query version, pack — so a Go-side change to
  how results are *read* invalidates nothing, and re-analysis of an unchanged commit is skipped. The
  seed lane already solves this with a build-stamped extractor version; the expand lane has no
  equivalent. **The decision is a flag, a build-stamped constant, or a hash of the interpreting
  code.** See [`db/unresolved-references.md`](db/unresolved-references.md).
- **The ownership gate is inert on an existing install until somebody refreshes.** A declaration
  roots nothing unless a recorded token scope says the membership list could have been complete, and
  a corpus verified before the `scopes` column carries NULL everywhere, so nothing can be
  contradicted. Conservative by design. **The decision is whether the server prompts, refreshes on
  start, or leaves it alone.**
- **An edge records which analysis produced it, and the Explorer ignores it.** `refnodes.go`
  attributes every edge leaving a dependency to the **most recently analyzed** reference — right
  whenever the newest analysis is the one whose rows survive, silently wrong for an edge only an
  older reference produces. **`store.SubmissionClosure` already does it properly**, joining
  `query_run.db_commit` to the frontier's `resolved_sha`, which corrected a live submission. The
  Explorer could be made exact by the same join.
- **A finding nobody attributed is invisible to a policy.** The evaluation runs through
  `abom_finding_node`, so a finding no node carries cannot be refused by a gate. Measured on the
  retired corpus: **14 findings across 3 repositories were linked to no node**, 8 of them in
  `has-ghas/codeql`. **The cause is now reproducible rather than counted**
  (`internal/ingest/marker_test.go`, on CLI-generated fixtures): a composite action that names no
  `uses:` is **not a node at all**, because the graph models what a `uses:` line touches — so a
  finding reported on it attaches to nothing. The same fixture shows the second half: a finding on
  a job lands on its *workflow*, because `store.LinkFindingsToNodes` matches by `path` and only its
  first pass matches a line. **The decision is whether attribution should be repaired — which needs
  a line range on the node, a schema change — or whether an unattributable finding is a permanent
  class the gate names and steps around.** A crawl seeded with the experimental marker is the right
  thing to re-measure against: the marker reports on every job and composite action, so the share it
  fails to attach sizes the gap directly rather than by inference. Take the number once a round has
  finished, because attribution runs at the end of one.
- **Seeded repositories in no declared account.** The interface no longer misreports them as owned.
  **The decision is whether such a repository is pruned, kept as coverage, or labeled orphaned.**
  A full listing now counts the same thing from the other direction — `missing` on an enumeration
  report names how many repositories the corpus holds that the account no longer lists — and it
  deliberately removes none, because that is the same undecided question.
- **A configured property key can group an organization into a unit nobody chose.** GitHub lets a
  custom property be required with a default, so every repository carries a value and none is
  unset. Measured on the reference organization: `business_id` places **8 of 25 repositories in the
  default `0`**, and `phi-scan` places all 25 in `scan-pending`. The breakdown marks the declared
  default so it can be told from a choice. **The decision is whether a default bucket may scope a
  policy, a campaign or a report at all**, or whether being in one is grounds for asking somebody to
  pick.
- **Three places show a reader a number they cannot reconcile.** The Coverage unit sorts by
  score while the crawl claims by depth, so the order shown is not the order that will happen.
  Discovery's tier chips and its objective panel decompose one queue two ways that do not sum alike,
  with nothing saying why. And the Bill of Materials states how many findings lack a run key, hash or
  digest without letting a reader see which run produced any given finding.
  **A fourth:** `expand-all` ends with "N repositories", and N is the number of analyses — a crawl
  reporting 319 left 101 rows in `repository` and 317 in `abom_query_run`. The word is wrong rather
  than the count.
- **`abom_surface` is a read model nothing writes to.** It declares `permissions`,
  `permissions_scope`, `triggers`, `runs_on`, `uses_secrets_inherit`, `is_privileged` and
  `is_externally_trigger` — note the truncated column name — and holds **0 rows** on a corpus whose
  `abom_observation` carries all seven facts, 21,098 rows of them, keyed to `job` nodes. Anything
  built against the table would render seven blank columns. **The decision is whether it is
  populated and becomes the fast path for the job and workflow columns, or dropped.** Until then the
  observation table is the only source, and a reader of the schema has no way to tell.
- **`org:$` is a parse artifact presented to a reader as an account.** An unexpanded `${{ }}`
  expression promoted to an owner; `org:docker:` carries a trailing colon.
  [`db/unresolved-references.md`](db/unresolved-references.md) owns the root cause and does not
  mention that it reaches the graph as an organization. Name them as synthetic, or stop promoting
  them.

## Reasoning worth keeping

[`plans/implemented.md`](implemented.md) is **deliberately git-ignored**. What git history does not
carry is the reasoning — the measurement that refuted a design, the number that did not move and why
that was right — and that lives there for whoever works here next. It may not exist on a fresh
clone, which means nothing has been recorded on this machine, not that nothing has been built.
