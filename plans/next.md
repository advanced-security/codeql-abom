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

1. **[An analysis says how its database was built](db/analysis-mode.md)** — cheap, and a hard
  prerequisite of [the closure database](ql/closure-database.md) rather than a companion to it.
   Every database ABOM has built is one
   repository's own checkout, which is the shape Default Setup builds; a staged closure answers a
   different question with the same rule ids. Without `regular` versus `extended` on the row, reach,
   the bill of materials, the submission closure and the pinning counts all silently start mixing
   two meanings, and none of them would report that its inputs had changed. **The term is owed to
   the interface too** — a reader has to be able to tell *somebody will see this alert* from
   *nobody will ever alert on this, and it still reaches you*.
2. **[Analyze the commit the workflow actually locks](db/actions-lockfile.md)** - the default
  resolution contract going forward, not an optional acceleration. Read the native lock at the
  analyzed revision, keep consumer/workflow context, and select its immutable commit on cache
  hits and cold fetches alike. Two consumers can lock the same tag differently; today's global
  frontier slot and a live-tag probe cannot represent that. Start with the official parser and
  nested-lock precedence spike, sharing the compatibility matrix already recorded in
  [tools/actions-lock.md](tools/actions-lock.md) without waiting for a hosted rollout. Invalid
  locks never silently become current-tag resolution.
3. **[One database per owned repository, holding its whole closure](ql/closure-database.md)** — an
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
   resolution lands. Depends on [analysis mode](db/analysis-mode.md) and [lock-aware
   resolution](db/actions-lockfile.md); lockfiles do not remove staging collisions.
4. **[What the extended analysis reveals, and whether any number should move](db/extended-findings.md)**
    [The closure database](ql/closure-database.md) is built to change nothing; this is where the
   decision to let it change something gets
   made, once there is something to look at. Reach is computed from `abom_edge`, so extended rows
   landing there without a decision would reorder the crawl, resize every node the Explorer draws
   and move half the remediation arithmetic **without anybody choosing it**. The deferral only stays
   open while its default changes nothing, which is the invariant this entry exists to hold. Its
  first item is looking at the data, and *no change* is a legitimate outcome. Depends on
  [the closure database](ql/closure-database.md).
5. **[A unit is a node kind, and its columns are its own](ui/inventory-units.md)** — The rule holds for what a row
   says and not for what a reader can do with it. **Four actions sit above the unit strip, so all
   five units offer all four**: three bill-of-materials exports that ignore which unit they were
   pressed on, and *show these in the graph*, which hands the scope to a surface that draws nodes —
   and an advisory is not a node, nor is a repository. The bill of materials is also still a page
   that is neither a surface nor a unit, holding coverage and hop-depth claims no listing makes,
   reachable only by an address the navigation does not offer.
6. **[A term is enforced or refused, and today one is neither](api/filter-enforcement.md)** — it
   stopped being cosmetic. It still holds `abom_policy.scope_kind`, a second
   narrowing mechanism no reader can see. It now also holds the language's inability to say that a
   list is deliberately empty, so *every finding flag off* is a view nobody can share; and the
   question of which side applies each term, now that a third side is declared for listings. The
   divergence that was feared is not there — server and browser select **identical node sets** for
   `minReach`, `origins` and `ref` on the crawled organization — so what is owed is writing the
   split down, not repairing it. **A scalar term's value is now enforced**; `kinds`, `origins`,
   `edges` and `flags` are lists whose members are still unchecked, and whether partial validity in
   a list refuses or warns is this plan's decision. **Two more terms are arriving** — `mode` from
  [analysis mode](db/analysis-mode.md) and `origin` from [the query catalog](db/query-catalog.md) -
  and each is declared on every side or it will drift.
7. **[Hiding something is a decision somebody signs](db/exclusions-and-exceptions.md)** — every
   organization carries things it has decided not to look at and things it has decided not to act on
   yet. `abom_policy_waiver` already models both, with a required reason, an author, and an expiry
   that is required for an exception and read when the decision is applied. It reaches nothing but a
   policy verdict. What is owed is a scope beyond one policy, an exclusion that actually stops work,
   an exception that hides a result while the work still runs and records, and a screen that refuses
   to write either without a reason and a review date.
8. **[A container image is not an account](db/container-images.md)** — `uses: docker://alpine:3.20`
    is parsed correctly, marked external and given no owner, and the graph then groups it by
    splitting its key on the first slash — which for `docker://` is the second character of `//`.
    Every container image in the corpus is drawn inside a boundary labelled `docker:`, an account
    nobody can be a member of and nobody can declare. The same fall-through once had ABOM reporting
    third-party dependencies on an account named `$`, so the fix is to refuse the branch rather than
    to correct its output. Cheap, visible, and startable against the corpus that exists.
9. **[Only one read still scales with the organization](api/org-enumeration.md)** — the listing is
    bounded by what moved and **the per-repository CodeQL probe is not**: there is no code-scanning
    field on `Repository` in the GraphQL schema, checked by introspection, so it is one REST request
    each. At twenty thousand repositories that is now the entire bill. The same entry owes the
    interface the state the API already carries — which route answered, when the account was last
    listed in full, when last swept.
10. **[Use GitHub's tooling to keep workflow locks current](tools/actions-lock.md)** — ranked here
  because what is left needs an approved target rather than a decision. Enforcement is settled on
  this repository: a `uses:` the lock does not account for is refused before any job starts, and a
  run log shows the runner selecting the locked commit for a tag ref, so the pinned CLI and its
  offline coverage check now gate every push and `make verify`. What no local check can supply is
  which platforms that toolchain combination is good on, and what a Dependabot update does to a
  workflow and its lock together — the same blocker [the native coverage
  crosscheck](api/dependency-graph-crosscheck.md) carries, which is why the two sit
  together. Approved platform experiments cannot be replaced by a run-details link.
  GitHub owns generation and verification, not a second ABOM pinning tool.
11. **[Measure native Actions coverage before filling a gap](api/dependency-graph-crosscheck.md)** -
  native lock graphs and Dependabot updater support do not prove deployed DG/SBOM ingestion,
  advisory alerts or update PRs. Compare those capabilities independently on an approved clean
  target, without historical custom submissions masquerading as native data. Also compare source
  extraction at identical commits using CodeQL, native lock evidence and the reference action's
  parser. A default-branch SBOM still cannot replace a pinned revision. Publication expansion
  waits for a concrete residual gap; this comparison does not promise one exists.
12. **[A query has an origin, and a finding remembers it](db/query-catalog.md)** — three kinds of
    query already run and nothing tells them apart: `abom_finding.provider` records the SARIF tool
    driver, which is `CodeQL` for upstream, ABOM and operator-written queries alike. The only
    available channel is the `abom/` prefix on a rule id — a **convention**, enforced by an
    instruction file rather than by the data, offered as though it were provenance. It also picks up
    the two columns that already exist and are **never written**, `query_version` and `pack_version`,
    without which *what has not run since pack X at vY* needs every suite re-resolved. Depends on
    [analysis mode](db/analysis-mode.md).
13. **[A scope names a set of databases, not only a set of nodes](api/scope-to-databases.md)** — one
    predicate already means the same thing on three surfaces, and none of them can answer *which
    databases would have to be analyzed to settle this*, which is what every re-analysis and every
    simulation actually asks. `store.NodesMatching` returns nodes; getting to a run needs a
    repository, a commit and a database, and nothing assembles those from a `Spec`. **Does not wait
    on [the closure database](ql/closure-database.md)** - a regular per-repository database is a
    database, and the extended shape joins the
    same resolution later behind the `mode` term. Depends on [the query catalog](db/query-catalog.md).
14. **[Run these queries, against this scope, now](api/on-demand-analysis.md)** — the only way to
    run anything is to requeue a whole tier and wait for the crawl to reach it, which is the right
    shape for keeping an organization current and the wrong shape for every question a person
    arrives with. A crawl is not a feedback loop, and a feedback loop is what writing a query needs.
    Execution goes through the lanes `internal/expand` already has, because a second execution path
    would drift and the two would disagree about what a query means. Depends on
    [scope to databases](api/scope-to-databases.md).
15. **[A policy can only say what the filter language can say](api/policy-language.md)** — down to
    the half that matters: the language cannot say *carries a live advisory* or *is reached from
    something you own*, which is most of what an Actions rule is for. Both terms are unblocked and
    depend on nothing; each needs adding to the enforceable declaration in `internal/view` or the
    set path refuses it. **Above the gate deliberately, because the gate enforces whatever this
    says** — and the vision now names these terms as what a rule is about.
16. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM can apply
    declared policy to an organization's recorded transitive dependency evidence.
    **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
    remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
    `store.EvaluatePolicy` rather than a second engine. The ground under it is firmer than it was:
    exposure and the submission closure now agree about which version a route belongs to, held by
    tests for the existing declared-ref path. Effective base/head comparison now also needs
    [lock-aware resolution](db/actions-lockfile.md)'s separate lock contexts. Depends on
    [the policy language](api/policy-language.md) for what a rule may say. Its verdict-only client
    remains deferred in [api/abom-check.md](api/abom-check.md).
17. **[A commit-pinned action can still be named as a version](db/version-resolution.md)** - measured
    crawl mappings already feed versions into the existing submission path. Remaining lookup fills
    version-evidence gaps, including native lock refs whose release association is unverified;
    it never substitutes a live tag for [lock-aware resolution](db/actions-lockfile.md)'s selected
    commit. Direct-pin publication and upstream aliases remain deferred until [the native coverage
    crosscheck](api/dependency-graph-crosscheck.md) establishes a native gap.
18. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — unblocked.
    The sweep was written against a corpus where nothing had ever been sighted, so it correctly
    condemned nothing and proved nothing. There are now **1,724 sighting rows** from a crawl that ran
    the findings suites, so the tombstone and the `_live` view can finally be exercised against a
    finding that really does stop being reported.
19. **[The same graph draws a different picture every time](ui/graph-layout.md)** — the force
    simulation has no fixed seed, so no two runs agree and nothing downstream can rest on a
    position: the screenshot layer and the browser suite's geometry claims are both looking at
    something different every time, which is why a test named *switching force preserves angle*
    exists at all. **The server half is built** — DOT export, cluster subgraphs, a Graphviz seam
    that degrades by name, and `/api/graph/layout` — and nothing in the browser reads it yet, so
    what a reader sees has not moved. **Not a performance fix, and must not be sold as one**:
    removing every edge from the whole-corpus view still reaches 4.8 FPS, so the ceiling is the
    renderer. Two claims the corpus refuted on contact, both recorded in the plan: cluster boxes
    are **not** non-overlapping by construction — that is a property of `dot`'s ranking, and
    `neato` crossed all 21 pairs — and the engine whose canvas is best shaped is the least legible
    of the set, which an aspect-ratio test selected before a render disqualified it.
20. **[Remediation summarizes two things into their more reassuring halves](ui/remediation-honesty.md)**
    — down to one half. The strip-versus-table gap is closed: the advisories unit carries an
    `unreached` count and states it under the table, so **3 reaching you** and **8 reaching
    nothing** are both on screen and sum to the 11 the strip counts. What is left is `Fix:
    available`, one boolean over an advisory whose ranges include **no fix published**, with 5
    repositories sitting in exactly that range — a reader is still told the comfortable half.
21. **[Reach counts a repository that pins a version without the dependency](db/reach-version-constraint.md)**
    — exposure and the submission closure now agree about which version a route belongs to; reach
    carries none of it, and reach is what orders the crawl. **132 of 225** dependencies are analyzed
    at more than one reference. It sits below the interface entries because it is invisible to
    everyone and its first step is a decision that may legitimately conclude no change.
22. **[A backup is a command, not a thing everybody is told to remember](db/backup-and-restore.md)**
    — the correct incantation is written in the working-style instructions, printed by `make e2e`
    when it refuses the working database, and implemented nowhere. Getting it wrong raises no error:
    `cp` does not read the WAL, so the baseline is silently missing recent commits and a change that
    did nothing looks the same as a change that worked. The half that is a correctness fix rather
    than an ergonomic one is the epoch — a restore moves generation history backwards underneath
    every connected client, and nothing currently tells them to reload.
23. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate already holds new
    findings at zero in all four languages. What is left is complexity: **50 production functions
    over 10**, and `runServe` is the one that keeps tripping the limit when anything is added to it.
    Measured from source, so unaffected by the corpus retirement. Cleanup rather than risk
    reduction, which is why it sits here.
24. **[The coverage strip orphans a cell](ui/theme.md)** — with nothing declared the strip renders
    four items into a three-column grid, so the fourth sits alone on a second row of every surface
    that carries it: Inventory, the Explorer, Settings and the bill of materials. Only the 4 and 5
    item cases are affected; a populated corpus renders 6 and divides cleanly, which is why this is
    invisible in exactly the runs most likely to be looked at. Held by a `test.fail()` that will
    break the run when it is fixed.
25. **[A query is written from an example, not from QL](ql/query-authoring.md)** — the set of
    questions anybody can ask of the graph is fixed: whatever `codeql/actions-queries` ships plus
    one marker ABOM writes. Those are good general questions and they are not *this organization's*
    — which runner labels are permitted, which internal action must never sit beneath a privileged
    job. CodeQL expresses all of them; what stops a security team writing them is QL, the `actions`
    library and a fixture, and the person who knows the Actions patterns usually has none of the
    three. The [CodeQL development MCP server](https://github.com/advanced-security/codeql-development-mcp-server)
    already automates that loop and already enforces the rule this repository has been burned by
    breaking: **the fixture comes from the CLI at every step**. **Does not need
    [the closure database](ql/closure-database.md).** Depends on [the query catalog](db/query-catalog.md).
26. **[A query's effect is known before anybody turns it on](api/query-simulation.md)** — a policy
    is simulated before it is adopted and a query is not, and the query is the more consequential
    of the two: publishing one changes what every developer in the organization sees, and its author
    is not who gets asked about the four hundred alerts it raised. **The prediction is sourced from
    `regular` mode and only from `regular` mode**, because Default Setup stages nothing and will
    never see what [the closure database](ql/closure-database.md) reveals - which is what
    [analysis mode](db/analysis-mode.md) exists for, arriving where it matters
    most. Exact for the commit analyzed, an inference beyond it, and it says which. Depends on
    [on-demand analysis](api/on-demand-analysis.md) and [query authoring](ql/query-authoring.md).
27. **[A query leaves ABOM as a published pack, never as an alert](ql/pack-promotion.md)** — the
    upload route is **refused rather than deferred**: it needed re-anchoring, stable fingerprints, a
    commit on every finding, a tool-and-category pair whose mishandling *deletes somebody's alerts*,
    and a confirmation gate in front of all of it. A configuration file applied through the
    `github-codeql-config-file` repository property is merged with what Default Setup generates, so
    the organization adds a pack through its own consent mechanism and ABOM writes nothing. **No
    alert exists that a person did not choose**, now satisfied by construction. Its first item —
    publishing ABOM's own four packs to GHCR - depends on nothing; the rest depends on
    [query simulation](api/query-simulation.md).
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
supplied the compatibility matrix it needs. [Analysis mode](db/analysis-mode.md) still precedes
any extended result, and the
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

**The ordering is a decision, not drift.** Mode, native resolution and the closure investigation
sit above long-standing interface work because selecting the right code and distinguishing its
analysis shape must precede new derived results. The checkout cache is built and reused, not
another implementation to insert into the ranking; its retention default remains a measured
refinement. [The query catalog](db/query-catalog.md), [scope to
databases](api/scope-to-databases.md) and [on-demand analysis](api/on-demand-analysis.md) sit above
the policy and gate work because a gate enforces rules,
and the rules an organization wants are ones nobody can write yet. **[Query
authoring](ql/query-authoring.md), [query simulation](api/query-simulation.md) and [pack
promotion](ql/pack-promotion.md) stay below the gate and interface work and are not blocked by
[the closure database](ql/closure-database.md)** - a query authored against a regular
per-repository database is the useful default and is what Default Setup would run anyway; the
extended shape widens what a query can see and is not a precondition for asking one. The enumeration
and crosscheck work beneath is no less urgent than it was. Disagreeing with that is the point of
writing it down.

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
- **[A report somebody can hand to somebody else](ui/printable-report.md)** - blocked on [graph
  layout](ui/graph-layout.md). A
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
| [api/query-suites.md](api/query-suites.md) | suite staleness, and streaming result sets out of BQRS; query identity belongs to [db/query-catalog.md](db/query-catalog.md) |
| [api/reachability.md](api/reachability.md) | all-routes reach, and `needs:` ordering nothing renders |
| [db/analysis-ledger.md](db/analysis-ledger.md) | what a run records, and how a change to any of it reaches work already on record |
| [db/data-efficiency.md](db/data-efficiency.md) | the standing cost review; one endpoint is still linear in rows |
| [db/declaration-provenance.md](db/declaration-provenance.md) | how a declaration arrived, and what contradicts it |
| [db/freshness-budget.md](db/freshness-budget.md) | claim order, and depth 2 starting before depth 1 drains |
| [db/observations.md](db/observations.md) | the fact table's keys and its readers |
| [db/owned-scope.md](db/owned-scope.md) | ownership edges the declaration does not yet settle |
| [db/pruning.md](db/pruning.md) | tombstones, compaction and what a delta reports |
| [db/ref-provenance.md](db/ref-provenance.md) | requested refs, governing locks and analyzed commits through the ledger |
| [db/supply-chain.md](db/supply-chain.md) | classification over the seed-reachable closure |
| [ui/tier-suites.md](ui/tier-suites.md) | per-tier suite binding, its precedence, and the control [api/on-demand-analysis.md](api/on-demand-analysis.md) needs |

## Gaps with a decision to make

- **A change can refuse a skip without being able to select the work it made stale.**
  `expand.pendingWork` now reads repository, commit, query id, pack digest, CodeQL version and the
  sparse filter the database was built from — but a refusal only moves work that is **queued**, and
  `store.RequeueStale` selects on `cli_version` and `pack_digest` alone. So widening
  `sparsePatterns` re-measures whatever happens to be on the queue and silently leaves everything
  already `done` measured the old way. A Go-side change to how results are *read* is worse: it is
  not a dimension at all, invalidates nothing, and re-analysis of an unchanged commit is skipped —
  the seed lane solves that with a build-stamped extractor version and the expand lane has no
  equivalent. **Two decisions, not one: how a Go interpretation change gets a version (a flag, a
  build-stamped constant, or a hash of the interpreting code), and what shape a requeue takes so
  that the next dimension does not need a third one.** `mode` from [analysis
  mode](db/analysis-mode.md) and the version columns
  from [the query catalog](db/query-catalog.md) are both already ranked to arrive. See
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
