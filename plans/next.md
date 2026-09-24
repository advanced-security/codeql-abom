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
  crawl have been removed rather than carried forward, because a remembered constant gets compared
  against instead of re-measured.
- **One plan cites another by name and path, never by rank.** A citation that encodes a position is
  wrong the moment anything above it moves, and nothing reports that it went wrong.

The ranking is itself a plan, and is subject to change.

## Ranked next

**QL-as-a-Service leads.** The first four entries are one loop: a request, a query an agent writes
and tests from its examples while ABOM confines and checks every step, a simulation over a real
scope, a published pack. The agentic part of the loop is built, and so is the way a reader follows
it, and [`docs/architecture.md`](../docs/architecture.md) says what both do and refuse. Running
chosen queries over a real scope is built; what it lacks is convenience, so it sits below the loop
beside the control it waits on.

1. **[A query is written from an example, not from QL](ql/query-authoring.md)** — a custom query is
   cataloged from disk, tested over **its own tests only, under the CodeQL the server runs**, and
   marked through seven steps derived from evidence; **`dev` → `test` and `test` →
   `extended-test` are both earned**, and a request's target is *selected*. **A test fails first
   on the lines its fixture marks**, and the tests step passes only once an earlier version failed
   there. An agent session writes the fixtures and the query, and an operator's approval makes its
   report the expected result before ABOM runs the tests itself; a report listing failed
   expectations cannot be accepted. One `actions-all` answers for ABOM's packs, the operator's and
   ql-mcp's. Left, and ahead of the rest of the loop because simulation and promotion both run or
   publish an operator's query: **a pack ABOM scaffolds cannot compile its tests or its queries
   until somebody runs `codeql pack install` in both directories**. Decided while the operator was
   unavailable: ABOM installs what it scaffolded, once, where no lock exists.
2. **[A query has an origin, and a finding remembers it](db/query-catalog.md)** — the catalog is
   keyed by **query version** over the query and its whole `.qll` import closure, takes origin from
   the pack, and lists a query from the moment it is on disk. None of that reaches a predicate:
   **the filter language has no term for a query's origin, and `origins` already means a
   repository's**, so item 1 is a naming decision with three named alternatives. And a finding says
   which rule fired, not whose rule it was, so *our rules against the maintained set* cannot be
   compared anywhere.
3. **[A query's effect is known before anybody turns it on](api/query-simulation.md)** — what earns
   `review`. A policy is simulated before it is adopted and a query is not, and the query is the
   more consequential: publishing one changes what every developer sees, and its author is not who
   gets asked about the alerts it raised. **The prediction is sourced from `regular` mode only**,
   because Default Setup stages nothing and will never see what [the closure
   database](ql/closure-database.md) reveals. Depends on the run [on-demand
   analysis](api/on-demand-analysis.md) already carries out, and on [query
   authoring](ql/query-authoring.md).
4. **[A query leaves ABOM as a published pack, never as an alert](ql/pack-promotion.md)** — what
   earns `deploy` beyond ABOM. The ABOM-controlled level is built: `deploy` rewrites a local suite a
   tier can bind. The upload route is **refused rather than deferred**; a configuration file applied
   through the `github-codeql-config-file` property lets the organization add a pack through its own
   consent, so **no alert exists that a person did not choose**. Its first item — publishing ABOM's
   own packs to GHCR, the actions library first — depends on nothing; the rest depends on [query
   simulation](api/query-simulation.md).
5. **[A scope kept on one page opens on every page that builds one](ui/named-scopes.md)** —
   decided: the Inventory, the Explorer and the CodeQL page each load a named scope and keep a new
   one, so a scope is portable rather than owned by the page that made it. Today only the Inventory
   does both; the Explorer offers named scopes only before anything is drawn and keeps none. One
   control lifted out of the Inventory's, with what each page can apply answered by the server.
   Directly below the loop and above [on-demand analysis](api/on-demand-analysis.md), whose run over
   a named scope takes this control; nothing here waits on lock-aware resolution.
6. **[Run these queries, against this scope, now](api/on-demand-analysis.md)** — built, and below
   the loop because what is left is convenience. A run over a scope covers exactly what the
   Repositories unit lists for it, *choose all* sends that scope from the Inventory, and the draft
   states the size before Run. Left: choosing a named scope
   on the CodeQL page, which takes [named scopes](ui/named-scopes.md)' control, gathering what a
   run still has to do while it runs — **the number and the list first**, and a time remaining
   only from the run's own pace, never forecast before Run — and **a size before Run that counts a
   database another CodeQL built as held**: on the verification store that is all 863, and the run
   rebuilds every one. A run builds the databases it lacks, or that another CodeQL built, records
   the CodeQL it ran under and skips what is already answered, which makes [the database
   cache](db/database-cache.md)'s item about a run evicting its own working set reachable.
7. **[Analyze the commit the workflow actually locks](db/actions-lockfile.md)** - the default
  resolution contract going forward, not an optional acceleration. Read the native lock at the
  analyzed revision, keep consumer/workflow context, and select its immutable commit on cache
  hits and cold fetches alike. Two consumers can lock the same tag differently; today's global
  frontier slot and a live-tag probe cannot represent that. Start with the official parser and
  nested-lock precedence spike, sharing the compatibility matrix already recorded in
  [tools/actions-lock.md](tools/actions-lock.md) without waiting for a hosted rollout. Invalid
  locks never silently become current-tag resolution.
8. **[One database per owned repository, holding its whole closure](ql/closure-database.md)** — an
   investigation with a measured spike and no implementation, ranked here because the measurement
   it rests on is cheap and may stop it. `codeql/actions-all` resolves a `uses:` by string equality
   against a callee staged at `.github/actions/external/${owner}/${repo}/`, so the namespace is flat
   and **depth is free**: three chained staged actions resolve, and the upstream
   `actions/code-injection/critical` reports a **seven-step taint path** across all three on default
   filters. Four hazards were measured and are refused by construction, not patched — bare sibling
   staging resolved one local reference to **two** callables, `.github/reusable_workflows/` staging
   manufactured a taint path into a file the repository does not contain, a staged reusable workflow
   is invisible without a recursive index filter, and a tree with no workflow anywhere resolves
  nothing at all. **The ceiling is one selected commit per dependency per database**, which is survivable
   only because the closure is rooted at one owned repository. Its headline verification is that
  **every existing total is byte-identical afterwards**, against the baseline after lock-aware
   resolution lands. Depends on [lock-aware resolution](db/actions-lockfile.md); lockfiles do not
   remove staging collisions.
9. **[What the extended analysis reveals, and whether any number should move](db/extended-findings.md)**
    [The closure database](ql/closure-database.md) is built to change nothing; this is where the
   decision to let it change something gets
   made, once there is something to look at. Reach is computed from `abom_edge`, so extended rows
   landing there without a decision would reorder the crawl, resize every node the Explorer draws
   and move half the remediation arithmetic **without anybody choosing it**. The deferral only stays
   open while its default changes nothing, which is the invariant this entry exists to hold. Its
  first item is looking at the data, and *no change* is a legitimate outcome. Depends on
  [the closure database](ql/closure-database.md).
10. **[A unit is a node kind, and its columns are its own](ui/inventory-units.md)** — down to one
   item, and it is a debt rather than a gap. Actions are split three ways now: the
   bill-of-materials exports sit behind *this scope:* and are **absent when nothing narrows**,
   because a label claiming a scope over the whole corpus is claiming a narrowing that does not
   exist; *show in Explorer* belongs to the unit and is disabled on Advisories with the reason on
   screen; and each row carries its own icon-only actions between Name and Type. The plan's own
   claim was refuted on contact — a repository **is** a node, 290 of them, and so is a job; only an
   advisory is not, and there are **seven** units rather than five. **The dedicated
   bill-of-materials page is deleted**, for the second time, and three of the four claims it alone
   stated — coverage, direct-versus-indirect depth, *not examined* against *none found* — are now
   made nowhere. The fourth already reads on the Actions unit and is the model for re-homing them.
11. **[A term is enforced or refused, and today one is neither](api/filter-enforcement.md)** — it
   stopped being cosmetic, and it grew two items that a control made visible. It still holds
   `abom_policy.scope_kind`, a second narrowing mechanism no reader can see; the language's
   inability to say that a list is deliberately empty, so *every finding flag off* is a view nobody
   can share; and the question of which side applies each term. The divergence that was feared is
   not there — server and browser select **identical node sets** for `minReach`, `origins` and `ref`
   on the crawled organization — so what is owed is writing the split down, not repairing it.
   **A scalar term's value is enforced**; `kinds`, `origins`, `edges` and `flags` are lists whose
   members nothing declares, which is now two defects rather than one: the server accepts a member
   outside the set, and a control has no list to offer because nothing says what the set is.
   Reading the corpus solved `rules` and `kinds` for the control and cannot solve the refusal,
   because what has been used is not what is allowed. And **a value list that does not say what it
   leaves out overstates itself**: `provider` offers one value covering 884 of 11,806 findings. The
   implemented `mode` term is enforced or refused on each side; `origin` from [the query
   catalog](db/query-catalog.md) must follow the same contract.
12. **[Hiding something is a decision somebody signs](db/exclusions-and-exceptions.md)** — every
   organization carries things it has decided not to look at and things it has decided not to act on
   yet. `abom_policy_waiver` already models both, with a required reason, an author, and an expiry
   that is required for an exception and read when the decision is applied. It reaches nothing but a
   policy verdict. What is owed is a scope beyond one policy, an exclusion that actually stops work,
   an exception that hides a result while the work still runs and records, and a screen that refuses
   to write either without a reason and a review date.
13. **[A container image is not an account](db/container-images.md)** — `uses: docker://alpine:3.20`
    is parsed correctly, marked external and given no owner, and the graph then groups it by
    splitting its key on the first slash — which for `docker://` is the second character of `//`.
    Every container image in the corpus is drawn inside a boundary labelled `docker:`, an account
    nobody can be a member of and nobody can declare. The same fall-through once had ABOM reporting
    third-party dependencies on an account named `$`, so the fix is to refuse the branch rather than
    to correct its output. Cheap, visible, and startable against the corpus that exists.
14. **[Only one read still scales with the organization](api/org-enumeration.md)** — the listing is
    bounded by what moved and **the per-repository CodeQL probe is not**: there is no code-scanning
    field on `Repository` in the GraphQL schema, checked by introspection, so it is one REST request
    each. At twenty thousand repositories that is now the entire bill. The same entry owes the
    interface the state the API already carries — which route answered, when the account was last
    listed in full, when last swept.
15. **[Use GitHub's tooling to keep workflow locks current](tools/actions-lock.md)** — ranked here
  because what is left needs an approved target rather than a decision. Enforcement is settled on
  this repository: a `uses:` the lock does not account for is refused before any job starts, and a
  run log shows the runner selecting the locked commit for a tag ref, so the pinned CLI and its
  offline coverage check now gate every push and `make verify`. What no local check can supply is
  which platforms that toolchain combination is good on, and what a Dependabot update does to a
  workflow and its lock together — the same blocker [the native coverage
  crosscheck](api/dependency-graph-crosscheck.md) carries, which is why the two sit
  together. Approved platform experiments cannot be replaced by a run-details link.
  GitHub owns generation and verification, not a second ABOM pinning tool.
16. **[Measure native Actions coverage before filling a gap](api/dependency-graph-crosscheck.md)** -
  native lock graphs and Dependabot updater support do not prove deployed DG/SBOM ingestion,
  advisory alerts or update PRs. Compare those capabilities independently on an approved clean
  target, without historical custom submissions masquerading as native data. Also compare source
  extraction at identical commits using CodeQL, native lock evidence and the reference action's
  parser. A default-branch SBOM still cannot replace a pinned revision. Publication expansion
  waits for a concrete residual gap; this comparison does not promise one exists.
17. **[A policy can only say what the filter language can say](api/policy-language.md)** — down to
    the half that matters: the language cannot say *carries a live advisory* or *is reached from
    something you own*, which is most of what an Actions rule is for. Both terms are unblocked and
    depend on nothing; each needs adding to the enforceable declaration in `internal/view` or the
    set path refuses it. **Above the gate deliberately, because the gate enforces whatever this
    says** — and the vision now names these terms as what a rule is about.
18. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM can apply
    declared policy to an organization's recorded transitive dependency evidence.
    **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
    remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
    `store.EvaluatePolicy` rather than a second engine. The ground under it is firmer than it was:
    exposure and the submission closure now agree about which version a route belongs to, held by
    tests for the existing declared-ref path. Effective base/head comparison now also needs
    [lock-aware resolution](db/actions-lockfile.md)'s separate lock contexts. Depends on
    [the policy language](api/policy-language.md) for what a rule may say. Its verdict-only client
    remains deferred in [api/abom-check.md](api/abom-check.md).
19. **[A commit-pinned action can still be named as a version](db/version-resolution.md)** - measured
    crawl mappings already feed versions into the existing submission path. Remaining lookup fills
    version-evidence gaps, including native lock refs whose release association is unverified;
    it never substitutes a live tag for [lock-aware resolution](db/actions-lockfile.md)'s selected
    commit. Direct-pin publication and upstream aliases remain deferred until [the native coverage
    crosscheck](api/dependency-graph-crosscheck.md) establishes a native gap.
20. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — unblocked.
    The sweep was written against a corpus where nothing had ever been sighted, so it correctly
    condemned nothing and proved nothing. There are now **1,724 sighting rows** from a crawl that ran
    the findings suites, so the tombstone and the `_live` view can finally be exercised against a
    finding that really does stop being reported.
21. **[The same graph draws a different picture every time](ui/graph-layout.md)** — down to one
    item, and it is a decision a reader has to settle rather than work. The browser draws where the
    engine placed it, **fitted to the nodes it is actually drawing**, and names which layout is on
    screen; the view refuses above **300 nodes** without explicit approval and there is **no
    arrival scope at all**. **The canvas measurement now exists and narrows the decision**: every
    built-in places **4 ranks deep**, because an owner contains a repository contains a workflow
    that uses an action and that is the whole depth — so height is a constant 216 and width grows
    with the population, giving **102:1 at 99 nodes** and **2,776:1 at 1,723**. `rankdir=LR` only
    rotates that; the question is whether a ranked layout suits a graph this shallow. **Two of six
    built-ins cannot be placed at all** — `dot` abandons 3,702 nodes at its 10s deadline. **Still
    not a performance fix**: removing every edge from the whole-corpus view reaches 4.8 FPS, so the
    ceiling is the renderer.
22. **[Remediation summarizes two things into their more reassuring halves](ui/remediation-honesty.md)**
    — down to one half, and that half turns out to be **latent rather than live**. The
    strip-versus-table gap is closed: the advisories unit carries an `unreached` count and states
    it under the table, so **3 reaching you** and **8 reaching nothing** are both on screen and sum
    to the 11 the strip counts. What is left is `fixable`, one boolean over an advisory whose
    ranges include **no fix published**, with 5 repositories sitting in exactly that range — and it
    reaches **no column today**. That makes it cheaper to settle, not safer to ignore: the field is
    on the model, and the next surface wanting a Fix column will find it and believe it.
23. **[Reach counts a repository that pins a version without the dependency](db/reach-version-constraint.md)**
    — exposure and the submission closure now agree about which version a route belongs to; reach
    carries none of it, and reach is what orders the crawl. **132 of 225** dependencies are analyzed
    at more than one reference. It sits below the interface entries because it is invisible to
    everyone and its first step is a decision that may legitimately conclude no change.
24. **[A backup is a command, not a thing everybody is told to remember](db/backup-and-restore.md)**
    — the correct incantation is written in the working-style instructions, printed by `make e2e`
    when it refuses the working database, and implemented nowhere. Getting it wrong raises no error:
    `cp` does not read the WAL, so the baseline is silently missing recent commits and a change that
    did nothing looks the same as a change that worked. The half that is a correctness fix rather
    than an ergonomic one is the epoch — a restore moves generation history backwards underneath
    every connected client, and nothing currently tells them to reload.
25. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate already holds new
    findings at zero in all four languages. What is left is complexity: **50 production functions
    over 10**, and `runServe` is the one that keeps tripping the limit when anything is added to it.
    Measured from source, so unaffected by the corpus retirement. Cleanup rather than risk
    reduction, which is why it sits here.
26. **[The coverage strip orphans a cell](ui/theme.md)** — with nothing declared the strip renders
    four items into a three-column grid, so the fourth sits alone on a second row of every surface
    that carries it: Inventory, the Explorer, Settings and the bill of materials. Only the 4 and 5
    item cases are affected; a populated corpus renders 6 and divides cleanly, which is why this is
    invisible in exactly the runs most likely to be looked at. Held by a `test.fail()` that will
    break the run when it is fixed.
27. **[The crawl says what it is doing, wherever a reader is](ui/crawl-visibility.md)** — down to
    how Settings is grouped. The state, the queue, a problem marker and the refresh interval are on
    the bar of every page, a restart starts nothing unless somebody configured it to, and the suite
    binding is chosen on the CodeQL page and stated in Settings. What is left is that Settings
    holds four unrelated decisions in one scroll, and that an [on-demand
    run](api/on-demand-analysis.md) is visible only on the CodeQL page's Runs tab while it spends
    the same cores.
28. **[An action remembers its upstream without becoming it](db/fork-provenance.md)** — forks and
    naming-convention mirrors need attributed upstream context without replacing the declared
    dependency or inventing a `uses:` edge. Same-host identity collection can start independently;
    cross-host lookup consumes [GitHub hosts](api/github-hosts.md), and version explanations
    consume measured evidence from [version resolution](db/version-resolution.md).
29. **[A repository lookup names its host and its credential](api/github-hosts.md)** — Git, REST
    and `gh` currently choose destinations separately. Start with a shared host/identity contract
    and two-host tests, preserving one owned organization and destination-scoped credentials.
    Coordinate cache identity with the [checkout cache](db/checkout-cache.md), which keys a fetch by
    commit and sparse filter and carries no host in that key; advertising enterprise-host support
    still requires an authorized environment, not only mocks.
30. **[Record the container references Actions actually declares](db/container-inventory.md)** —
    job/service images, Docker action metadata and referenced Dockerfile `FROM` declarations are a
    broader inventory than the existing `docker://` edges. Begin with a literal-source coverage
    spike and the shared adapter contract; full dynamic/dataflow resolution is not a prerequisite.
    [Container images](db/container-images.md) stays the small grouping correction. No image
    execution, package scanning or GitHub
    container-alert claim is included.

**[Fork provenance](db/fork-provenance.md), [GitHub hosts](api/github-hosts.md) and [container
inventory](db/container-inventory.md) keep their relative priority.** They capture
lessons from Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission),
raised in [issue #18](https://github.com/advanced-security/codeql-abom/issues/18). Existing owners
hold action packaging, version lookup, submission and extraction comparisons rather than new plans
duplicating them. They supplement native locks: repository IDs do not establish ancestry, a host
field does not prove platform support, and action pins do not inventory containers. `abom-check`
remains deferred and verdict-only; publisher expansion awaits [the native coverage
crosscheck](api/dependency-graph-crosscheck.md)'s native comparison.

**[Lock-aware resolution](db/actions-lockfile.md) deliberately precedes closure staging.** It is
the default design,
not an optional speedup: staging the current tag instead of the workflow's locked commit would
analyze the wrong code. It no longer waits on the toolchain adoption work, which has already
supplied the compatibility matrix it needs. Extended results must preserve the implemented
[analysis-mode isolation](../docs/architecture.md#analysis-modes), and the
remaining plans keep their order. GitHub's native tooling owns generation, verification and
updates; the first steps here reuse its released contracts and expose unknown coverage rather than
guessing rollout.

**Everything above rests on data, and there are now two sets of it** — a crawled organization for
before/after diffs, and a deterministic fixture one that gates a pull request. The fixture set is
no longer a plan: it is built, it gates both empty and populated, and it carries **30 repositories
across three accounts, 1,600 nodes, 1,632 findings and 29 of the 35 rules the default suite can
report**. A closure database's headline claim is that every existing total is unchanged, and
[the closure database](ql/closure-database.md) can hold that claim against the post-resolution
baseline. [Inventory units](ui/inventory-units.md), [filter enforcement](api/filter-enforcement.md)
and [exclusions and exceptions](db/exclusions-and-exceptions.md) remain one
refactoring, split because they are three workstreams rather than three phases of one: the units
are built against the edge facts that now exist, the filter work is owed to the gate whatever the
interface does, and the governed exclusions are owed to the policy engine regardless.

**The ordering is a decision, not drift.** QL-as-a-Service leads because it is what lets an
organization ask its own questions, and the policy language and the gate below enforce rules nobody
can write without it. It sits above lock-aware resolution and the closure investigation, which make
existing answers more correct, because nothing in it waits on them — a query authored against a
regular per-repository database is the useful default and is what Default Setup would run anyway.
Within it, the order is what can start first. Native resolution and the closure investigation
still sit above the long-standing interface work, because selecting the right code and
distinguishing its analysis shape must precede new derived results. Two caches are built and
reused rather than implementations waiting to be inserted into the ranking: the [checkout
cache](db/checkout-cache.md) holds a fetch, the [database cache](db/database-cache.md) holds an
extraction, and both keep their retention default as a measured refinement. **[Scope to databases
has retired](../docs/architecture.md#reading-the-data-out)** — one predicate now resolves to a node
set and a database set through one enforcement path, so what was an entry is a fact. The
enumeration and crosscheck work beneath is no less urgent than it was. Disagreeing with that is the
point of writing it down.

**A lifecycle exists, and two of its rungs are earned.** A query version moves through `dev`,
`test`, `extended-test`, `review` and `deploy`, and every move records who made it. [Query
authoring](ql/query-authoring.md) earns the first two: `test` needs the version's own tests to pass
under the CodeQL the server runs,
`extended-test` every step the version carries, which a query an agent wrote and one written by
hand can both earn. `review` and `deploy` are still set by hand; [query
simulation](api/query-simulation.md) and [pack promotion](ql/pack-promotion.md) own what would earn
them, and [on-demand analysis](api/on-demand-analysis.md) produces the evidence gathered inside
`extended-test`. A version at an unearned rung is the same shape of claim as a measurement nobody
took.

**Investigate before refining.** [The closure database](ql/closure-database.md), [extended
findings](db/extended-findings.md) and [query simulation](api/query-simulation.md) each open with a
measurement rather than an
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
  rather than by obstacle. Its planned release is verdict-only, over exact base/head commits and
  their lock contexts. It performs no analysis or native fixing and has no submission authority.
  Scoped reads and a stable evidence contract precede release; publisher expansion is deferred
  pending the native capability comparison, not bundled with lock adoption.
- **[A coverage gap names its cause](api/ghas-enablement.md)** — needs a released v2 of
  `gh-ghas-audit`. The contract is its `schema_version`, never a branch, and pinning a version that
  does not exist yet means building against something that still moves.
- **[A retained database is an archive, not a directory](db/codeql-db-bundling.md)** — deferred by
  decision, with the measurement already taken: `codeql database bundle` turns 19.4 MB of retained
  database into a **264 KB** archive, and a restore answers `AbomEdges.ql` with the same 54 rows.
  What is missing is the other half of the trade — a restore costs 1.24 s against a 387 ms query,
  it cannot be skipped (every database command refuses an archive on 2.27.0), and extraction has
  never been timed against it, so adopting the archive now would trade a disk problem for a
  latency problem on an assumption. Its first item needs none of that: two thirds of every
  retained database is build logs nothing reads.
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
- **[Retain guarded submissions until native coverage is measured](api/dependency-submission.md)** -
  existing manual submission and its ledger remain built; new client modes, write grants and
  automation are deferred. [The native coverage crosscheck](api/dependency-graph-crosscheck.md)
  must demonstrate a residual gap before expansion is reopened.
  Native replacement would require a deliberate release of historical snapshots on approved
  targets, not simply removing code. No timer or automatic first submission is introduced.
- **[A reference is known, or it is unknown in a way that can be named](ql/semantic-model.md)** —
  unblocked for design, and wider than it was: dataflow crosses a `uses:` boundary at any depth once
  the callee is staged, so what [the closure database](ql/closure-database.md) gives it is real
  flow rather than a lone bare checkout. One
  bucket currently holds a literal matrix, an environment variable set two jobs earlier, and `uses:`
  built from `github.event`, which is an attacker choosing what runs. `abom_observation` already
  keeps both readings of a changed fact and already refuses to pick a winner between two, so the
  storage for this exists and the work is naming. Shaped so a second adapter — a Dockerized action's
  image, a JavaScript action's own dependencies, a shell script a composite action calls — is
  addable; those implementations are not in this plan. [Container
  inventory](db/container-inventory.md) owns container declarations,
  not image contents. The shared model remains unranked.
- **[Reach is computed one way, and nothing checks it](ql/reachability-crosscheck.md)** — blocked on
  [the closure database](ql/closure-database.md). One in-memory walk produces reach, and reach
  orders the crawl, sizes every node drawn and
  is half the remediation arithmetic. [The native coverage
  crosscheck](api/dependency-graph-crosscheck.md) is this argument one level down. A closure per
  owned
  repository is a better second derivation than one database for the account would have been: it is
  pinned at the references its root really uses, where a whole-account database would collapse
  **132 of 225** dependencies to one reference and then disagree for a reason about layout rather
  than about reach.
- **[A report somebody can hand to somebody else](ui/printable-report.md)** - still blocked on
  [graph layout](ui/graph-layout.md), for a different reason than before. A drawing that was
  different every run cannot be printed, and that half is fixed: a placed scope is byte-identical
  across runs. What cannot be printed now is a **161:1** canvas, and a scope wide enough to be
  worth reporting on may not be placeable at all. The bill of materials remains the other half: a
  document with no way to carry the picture, and three of the claims it alone used to state are
  currently made nowhere — see [ui/inventory-units.md](ui/inventory-units.md).

## Built, with refinements nobody has ranked

These **fifteen** plans describe workstreams that are **built and carrying real data**, and each
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
| [api/query-suites.md](api/query-suites.md) | suite staleness, and streaming result sets out of BQRS; query identity belongs to [db/query-catalog.md](db/query-catalog.md) |
| [api/reachability.md](api/reachability.md) | all-routes reach, and `needs:` ordering nothing renders |
| [db/analysis-ledger.md](db/analysis-ledger.md) | what a run records, and how a change to any of it reaches work already on record |
| [db/checkout-cache.md](db/checkout-cache.md) | flipping retention on by default, and eviction that reads the queue |
| [db/database-cache.md](db/database-cache.md) | the same two for retained CodeQL databases, and a run that must not evict its own working set |
| [db/data-efficiency.md](db/data-efficiency.md) | the standing cost review; one endpoint is still linear in rows |
| [db/declaration-provenance.md](db/declaration-provenance.md) | how a declaration arrived, and what contradicts it |
| [db/freshness-budget.md](db/freshness-budget.md) | claim order, and depth 2 starting before depth 1 drains |
| [db/observations.md](db/observations.md) | the fact table's keys and its readers |
| [db/owned-scope.md](db/owned-scope.md) | ownership edges the declaration does not yet settle |
| [db/pruning.md](db/pruning.md) | tombstones, compaction and what a delta reports |
| [db/ref-provenance.md](db/ref-provenance.md) | requested refs, governing locks and analyzed commits through the ledger |
| [db/supply-chain.md](db/supply-chain.md) | classification over the seed-reachable closure |
| [ui/tier-suites.md](ui/tier-suites.md) | per-tier suite binding, its precedence, and cost guidance for a tier's re-analysis |

## Gaps with a decision to make

- **A change can refuse a skip without being able to select the work it made stale.**
  `expand.pendingWork` now reads repository, commit, query id, pack digest, CodeQL version and the
  sparse filter the database was built from — but a refusal only moves work that is **queued**, and
  `store.RequeueStale` selects on `cli_version` and `pack_digest` alone. So widening
  `sparsePatterns` re-measures whatever happens to be on the queue and silently leaves everything
  already `done` measured the old way. A Go-side change to how results are *read* is worse: it is
  not a dimension at all, invalidates nothing, and re-analysis of an unchanged commit is skipped,
  by an on-demand run as much as by the crawl — the seed lane solves that with a build-stamped
  extractor version and the expand lane has no
  equivalent. **Two decisions, not one: how a Go interpretation change gets a version (a flag, a
  build-stamped constant, or a hash of the interpreting code), and what shape a requeue takes so
  that the next dimension does not need a third one.** The recorded `mode` and the version columns
  from [the query catalog](db/query-catalog.md) must also be selectable. See
  [`db/analysis-ledger.md`](db/analysis-ledger.md), which holds both as pending items. **It is here
  rather than in the ranking because the first is a fork nobody has taken**, and it is ranked the
  moment one is — the requeue shape is ordinary work after that, and 63 edges are already waiting
  on it.
- **The ownership gate is inert on an existing install until somebody refreshes.** A declaration
  roots nothing unless a recorded token scope says the membership list could have been complete, and
  a corpus verified before the `scopes` column carries NULL everywhere, so nothing can be
  contradicted. Conservative by design. **The decision is whether the server prompts, refreshes on
  start, or leaves it alone.**
- **An edge records which analysis produced it, and the Explorer ignores it.** `refnodes.go`
  attributes every edge leaving a dependency to the **most recently analyzed** reference — right
  whenever the newest analysis is the one whose rows survive, silently wrong for an edge only an
  older reference produces. `store.SubmissionClosure` constrains the existing declared-ref path by
  joining `query_run.db_commit` to the frontier's `resolved_sha`. Native locks require [lock-aware
  resolution](db/actions-lockfile.md)'s
  consumer-specific binding in both readers; copying the global join cannot make locked selection
  exact when two consumers choose different commits behind one tag.
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
