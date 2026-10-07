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

## Parallel priority

**[Installable releases and distributed Kubernetes execution](tools/deployment.md) starts alongside
the ranked work, not after it.** Release readiness deserves its own parallel effort because most
documentation and deployment packaging can be isolated to `docs/`, `plans/` and the new `infra/`
directory. Keep the README entry point, build integration and release workflows narrowly scoped.
The [source-install recipe](../docs/deployment/source.md) needs clean-machine qualification;
portable Linux/macOS amd64/arm64 release bundles are the first new deployment to qualify against
the plan's archive contract. Multi-stage GHCR images and the private Helm deployment follow
within the same workstream.

**This does not demote or reorder the shared-runtime work below.** Documentation, image builds,
chart templates and deployment tests can proceed against agreed contracts while other features
are implemented. Asset resolution and distributed execution still need coordinated changes to the
CLI, runner, store, queue and existing UI states. Agree those interfaces with the affected
workstreams and integrate in small tested changes; do not serialize the entire deployment effort
behind them or mistake parallel development for permission to skip its acceptance gates.

## Ranked next

**Closure databases are built, and inert.** Every reader that says what runs follows the commit a
workflow's lock selects ([how](../docs/architecture.md#locks)), and a closure stages exactly that,
recording what it finds as [extended evidence](../docs/architecture.md#analysis-modes) that no
existing total reads. The QL-as-a-Service loop — request, authoring, tests, a run over a named
scope, and a comparison with the alerts developers already see — is built as far as `review`;
[`docs/architecture.md`](../docs/architecture.md) says what each step does and refuses. **The
published pack is deferred until much later**, and checking a comparison against what appears once
it is live waits with it.

**The graph is being rebuilt around commits, for an enterprise.** By the operator's decision ABOM
is to represent many organizations in one instance, up to 100,000 repositories each, current as
commits land, with its own correctness measured continuously. Change ingestion is built — events,
polling and the sweep behind both, each kind of work on a metered pool — and the checker, the store
backends and the commit-aware graph come next for the same reason, and **everything after them
moved down without being re-judged**. The commit-aware graph was first of them; what is left of it is
small, though one part — upstream CI in the views — has a measured cost in every bill of
materials, and it stays last of the three only because walks and every total are unaffected. The
checker's internal checks are built. Declaring ownership by repository property, which the operator
ranked ahead of them, is built.

1. **[Take what code scanning already computed](api/code-scanning-reuse.md)** — ranked above the
   checker by the operator's decision: downloading what exists is cheaper than producing it
   again. A database stands in for a build only at the same commit, CodeQL and
   indexed files. An owned repository's analysis answers for each findings rule it ran, matched by
   rule id whatever CodeQL, pack, release or threat model ran it, where default setup made it; every
   rule records what produced it. Everything below moved down one place without
   being re-judged. Left: the crawl's totals with reuse on and off, a push waiting for code scanning,
   and Settings showing the counts the cycle log already prints.
2. **[Check the graph against the world, continuously](tools/graph-consistency-checker.md)** —
   `codeql-abom check` runs the internal checks exhaustively on the crawled organization in about
   36 s, files a per-build trend and a disagreement queue behind `/api/checks`, and names the checks
   it does not run. Its first run found 100 live edges under pruned nodes, and a run that names
   something removed now brings it back. Left: its pool, the external checks and check 5, which
   draw on token pools that are now built; check 6, the agent
   and the interface; and check 11, work that comes back unchanged, which spends nothing — a loop
   between two definitions of done held every claim for 3.7 hours before anyone saw it.
3. **[One store, two backends](db/store-backends.md)** — an enterprise deployment needs a replicated
   coordinator and scaled readers, which one embedded SQLite writer cannot be. Extracting the
   interface and the shared suite start now; SQLite stays first-class for the single binary.
4. **[A graph that knows which commit said what](db/commit-aware-graph.md)** — every walk and every
   one-version view now reads what each commit declared at the commits that are live, a starting
   point's own CI only at its default and protected branches, and reach and exposure are derived
   for what changed (**0.4 s** against a **19 s** rebuild, row for row equal). Left: upstream
   projects' own CI still fills the views — a one-workflow repository's bill of materials lists
   1,000 components where about six actions run, because the BOM walk enters every node of a
   component's repository — and owned-scope membership still reads the row, which moves nothing on
   the crawled organization and waits for a home for its per-state cost.
5. **[Hiding something is a decision somebody signs](db/exclusions-and-exceptions.md)** — every
   organization carries things it has decided not to look at and things it has decided not to act on
   yet. `abom_policy_waiver` already models both, with a required reason, an author, and an expiry
   that is required for an exception and read when the decision is applied. It reaches nothing but a
   policy verdict. What is owed is a scope beyond one policy, an exclusion that actually stops work,
   an exception that hides a result while the work still runs and records, and a screen that refuses
   to write either without a reason and a review date.
6. **[A container image is not an account](db/container-images.md)** — `uses: docker://alpine:3.20`
   is parsed correctly, marked external and given no owner, and the graph then groups it by
   splitting its key on the first slash — which for `docker://` is the second character of `//`.
   Every container image in the corpus is drawn inside a boundary labelled `docker:`, an account
   nobody can be a member of and nobody can declare. The same fall-through once had ABOM reporting
   third-party dependencies on an account named `$`, so the fix is to refuse the branch rather than
   to correct its output. Cheap, visible, and startable against the corpus that exists.
7. **[Use GitHub's tooling to keep workflow locks current](tools/actions-lock.md)** — ranked here
  for the compatibility matrix and supported-workflow documentation that can start locally.
  Record the pinned toolchain and existing evidence without treating this repository's observed
  enforcement as proof for other platforms or nested pins. New enforcement experiments and an
  observed Dependabot update of workflow and lock need an approved target, coordinated with
  [the native coverage crosscheck](api/dependency-graph-crosscheck.md). GitHub owns generation
  and verification, not a second ABOM pinning tool.
8. **[Measure native Actions coverage before filling a gap](api/dependency-graph-crosscheck.md)** -
  start with the isolated source-extraction comparison at identical commits using CodeQL, native
  lock evidence and the reference action's parser; it needs no submission or live test target.
  Deployed DG/SBOM ingestion, advisory alerts and update PRs require separate evidence on an
  approved clean target, without historical custom submissions masquerading as native data.
  A default-branch SBOM cannot replace a pinned revision. Publication expansion waits for a
  concrete residual gap; this comparison does not promise one exists.
9. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM can apply
    declared policy to an organization's recorded transitive dependency evidence.
    **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
    remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
    `store.EvaluatePolicy` rather than a second engine. Exposure and the submission closure agree
    about which version a route belongs to — declared, locked, and into called reusable workflows —
    held by agreement tests. Effective base/head comparison uses
    the [recorded lock contexts](../docs/architecture.md#locks), and a lock requirement is already
    a policy over the lock rules. What a rule may say
    is built: findings or advisories, how an owned repository reaches either, and other policies
    by name. Its verdict-only client
    remains deferred in [api/abom-check.md](api/abom-check.md).
10. **[A commit-pinned action can still be named as a version](db/version-resolution.md)** - measured
    crawl mappings already feed versions into the existing submission path. Remaining lookup fills
    version-evidence gaps, including native lock refs whose release association is unverified;
    it never substitutes a live tag for the commit a [lock selects](../docs/architecture.md#locks). Direct-pin publication and upstream aliases remain deferred until [the native coverage
    crosscheck](api/dependency-graph-crosscheck.md) establishes a native gap.
11. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — unblocked.
    The sweep has only run where nothing was sighted, so it has proved nothing. The crawled
    organization holds **10,192 finding sightings**, so the tombstone and the `_live` view can be
    exercised against a finding that really does stop being reported. **First, the report judges a finding by runs of
    its suite rather than of its rule**, and never by a crawl lane: on a copy of the verification
    store, two on-demand runs of one query each over 15 repositories moved it from 313 findings to
    712, 396 of the new ones for rules neither run evaluated.
12. **[The same graph draws a different picture every time](ui/graph-layout.md)** — a decision a
    reader has to settle rather than work, and a defect that is work. The browser draws where the
    engine placed it, **fitted to the nodes it is actually drawing**, and names which layout is on
    screen; the view refuses above **300 nodes** without explicit approval and there is **no
    arrival scope at all**. **The canvas measurement narrows the decision**: every
    built-in places **4 ranks deep**, because an owner contains a repository contains a workflow
    that uses an action and that is the whole depth — so height is a constant 216 and width grows
    with the population, giving **102:1 at 99 nodes** and **2,776:1 at 1,723**. `rankdir=LR` only
    rotates that; the question is whether a ranked layout suits a graph this shallow. **Two of six
    built-ins cannot be placed at all** — `dot` abandons 3,702 nodes at its 10s deadline. **Still
    not a performance fix**: removing every edge from the whole-corpus view reaches 4.8 FPS, so the
    ceiling is the renderer. The defect: an address carrying any *Group by* but Owner throws from
    the force-graph library's frame loop as it draws, and never finishes drawing — a shared link
    with a grouping, or a reload after opening a named scope kept with one. A second defect: a
    neighborhood drawn with Stable layout on is placed with its whole scope and collapses into a
    line, five nodes across 26,887 × 233. [The closure figure](ui/closure-figure.md)'s placement,
    which lays out a subset alone, is built for it to use. A third: a key ending in a backslash
    comes back from Graphviz under another name, so the export never places it.
13. **[Remediation summarizes two things into their more reassuring halves](ui/remediation-honesty.md)**
    — down to one half, and that half turns out to be **latent rather than live**. The
    strip-versus-table gap is closed: the advisories unit carries an `unreached` count and states
    it under the table, so **3 reaching you** and **8 reaching nothing** are both on screen and sum
    to the 11 the strip counts. What is left is `fixable`, one boolean over an advisory whose
    ranges include **no fix published**, with 5 repositories sitting in exactly that range — and it
    reaches **no column today**. That makes it cheaper to settle, not safer to ignore: the field is
    on the model, and the next surface wanting a Fix column will find it and believe it.
14. **[A backup is a command, not a thing everybody is told to remember](db/backup-and-restore.md)**
    — the correct incantation is written in the working-style instructions, printed by `make e2e`
    when it refuses the working database, and implemented nowhere. Getting it wrong raises no error:
    `cp` does not read the WAL, so the baseline is silently missing recent commits and a change that
    did nothing looks the same as a change that worked — and neither does a `.backup` taken through
    `immutable=1`, the form the data-efficiency review skill and the bench hint give. The half that
    is a correctness fix rather than an ergonomic one is the epoch — a restore moves generation
    history backwards underneath every connected client, and nothing currently tells them to
    reload.
15. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate holds new
    findings at zero in all five analyses. What is left is complexity: **125 production functions
    over 10, total excess 362**, most between 11 and 16. The worst are `Store.Migrate`,
    `findingReviewPlan` and `Supervisor.cycle`; the first and last wait on tests they do not have
    yet. Cleanup rather than risk reduction, which is why it sits here.
16. **[The crawl says what it is doing, wherever a reader is](ui/crawl-visibility.md)** — down to
    how Settings is grouped. The state, the queue, a problem marker and the refresh interval are on
    the bar of every page, a restart starts nothing unless somebody configured it to, and the suite
    binding is chosen on the CodeQL page and stated in Settings. What is left is that Settings
    holds four unrelated decisions in one scroll, and that an on-demand run is visible only on the
    CodeQL page's Runs tab while it spends the same cores.
17. **[An action remembers its upstream without becoming it](db/fork-provenance.md)** — forks and
    naming-convention mirrors need attributed upstream context without replacing the declared
    dependency or inventing a `uses:` edge. Same-host identity collection can start independently;
    cross-host lookup consumes [GitHub hosts](api/github-hosts.md), and version explanations
    consume measured evidence from [version resolution](db/version-resolution.md).
18. **[A repository lookup names its host and its credential](api/github-hosts.md)** — Git, REST
    and `gh` currently choose destinations separately. Start with a shared host/identity contract
    and two-host tests, preserving declared ownership and destination-scoped credentials.
    Coordinate cache identity with the [checkout cache](db/checkout-cache.md), which keys a fetch by
    commit and sparse filter and carries no host in that key; advertising enterprise-host support
    still requires an authorized environment, not only mocks.
19. **[Record the container references Actions actually declares](db/container-inventory.md)** —
    job/service images, Docker action metadata and referenced Dockerfile `FROM` declarations are a
    broader inventory than the existing `docker://` edges. Begin with a literal-source coverage
    spike and the shared adapter contract; full dynamic/dataflow resolution is not a prerequisite.
    [Container images](db/container-images.md) stays the small grouping correction. No image
    execution, package scanning or GitHub
    container-alert claim is included.
20. **[Reach is computed one way, and nothing checks it](ql/reachability-crosscheck.md)** —
    unblocked by closure databases, and ranked last because nothing a reader sees moves until its
    third item reports the comparison. A CodeQL walk over each owned repository's closure is a
    second derivation of reach sharing no code with the Go walk. A closure stages callables only,
    so both walks stop at the same leaves — every JavaScript and Docker action — and differ only
    where one is wrong or the other has no coverage, such as the 9 of 28 owned repositories the
    crawl never analyzed.

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

**GitHub's native tooling owns lock generation, verification and updates**; ABOM reuses its
released contracts and exposes unknown coverage rather than guessing rollout. A closure database
stages the commit a governing lock selects, never the current tag.

**Everything above rests on two data sets**: a crawled organization for before/after diffs, and
a deterministic fixture corpus that gates every pull request both empty and populated. A closure
database's claim — every existing total unchanged — is checked against the first, endpoint by
endpoint. [Exclusions and exceptions](db/exclusions-and-exceptions.md) is what remains of one
refactoring: a named scope already means the same thing on every surface, each of its terms
applied or refused by name, and every Inventory unit's columns and actions are its own.

**The ordering is a decision, not drift.** Declaring by property leads because the operator ranked
it first, ahead of the enterprise work. The commit-aware graph, its checker
and the store follow because the operator ranked enterprise scale and commit-awareness ahead of
everything else. Below them, exclusions lead because hiding something is the next claim an
organization makes about its own data, and nothing records who made it. The interface
entries follow because each is a claim a reader can already see and misread. The
[checkout cache](db/checkout-cache.md) and [database cache](db/database-cache.md) are built and
reused, each keeping its retention default as a measured refinement. Disagreeing with that is the
point of writing it down.

**A query version's `deploy` rung is still set by hand.** `test`, `extended-test` and `review`
are earned, as [`docs/architecture.md`](../docs/architecture.md#what-produces-the-data) states;
[pack promotion](ql/pack-promotion.md), deferred until much later, owns what would earn `deploy`
beyond ABOM. A version at an unearned rung is the same shape of claim as a measurement nobody took.

**Investigate before refining.** An entry above may be carrying a number that has since moved;
reconcile before starting. A measurement that answers *no change* is a result: on the crawled
organization the extended analysis found nothing the regular one had not, which settled what it
may move rather than leaving it open.

## Deferred, and why

Not ranked, because nobody can start them here. Each keeps its plan and is refined there; none is a
refusal.

- **[A query leaves ABOM as a published pack, never as an alert](ql/pack-promotion.md)** —
  deferred by decision until much later. The ABOM-controlled level is built: `deploy` rewrites a
  local suite a tier can bind, and stays a stage set by hand while this waits. The upload route is
  **refused rather than deferred**; a configuration file applied through the
  `github-codeql-config-file` property lets the organization add a pack through its own consent, so
  **no alert exists that a person did not choose**. ABOM's own packs currently resolve from a
  checkout of this repository. [Deployment](tools/deployment.md) owns bundling them for installed
  runtimes; that does not require or resume registry publication.

- **[A member declares the repositories they administer](db/owned-scope.md)** — nothing could
  read what it names. Every such repository measured is private, ABOM reads through its App, and
  only an organization owner can install an App asking for organization permissions. Resumes with
  an App installation on those repositories or a decision that the operator's own token may crawl
  them; ownership is already decided per repository, so it no longer waits on that.
- **[A client in the workflow, thin enough to trust](api/abom-check.md)** — deferred by decision
  rather than by obstacle. Its planned release is verdict-only, over exact base/head commits and
  their lock contexts. It performs no analysis or native fixing and has no submission authority.
  Scoped reads and a stable evidence contract precede release; publisher expansion is deferred
  pending the native capability comparison, not bundled with lock adoption.
- **[A coverage gap names its cause](api/ghas-enablement.md)** — needs a released v2 of
  `gh-ghas-audit`. The contract is its `schema_version`, never a branch, and pinning a version that
  does not exist yet means building against something that still moves. The `actions` gap it was to
  contribute ABOM now closes itself; [`docs/architecture.md`](../docs/architecture.md) says how.
- **[A retained database is an archive, not a directory](db/codeql-db-bundling.md)** — deferred by
  decision, with the measurement already taken: `codeql database bundle` turns 19.4 MB of retained
  database into a **264 KB** archive, and a restore answers `AbomEdges.ql` with the same 54 rows.
  What is missing is the other half of the trade — a restore costs 1.24 s against a 387 ms query,
  it cannot be skipped (every database command refuses an archive on 2.27.0), and extraction has
  never been timed against it, so adopting the archive now would trade a disk problem for a
  latency problem on an assumption. Its first item needs none of that: two thirds of every
  retained database is build logs nothing reads.
- **[An address the enterprise can reach, an identity it granted, and readers GitHub
  authorizes](api/enterprise-server.md)** — ABOM runs as a private, read-only GitHub App with
  installation tokens and installations as evidence, re-read on installation events; a workstation
  receives its deliveries through a relay, verified end to end on `has-ghas`. Deferred: a production
  address needs ingress this workstation lacks, and readers signing in through the App and workflow
  callers wait on it. Installing on a second account waits on a decision the plan names: a private
  App installs only on its owner.
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
  the callee is staged, so what a [closure database](../docs/architecture.md#analysis-modes) gives
  it is real flow rather than a lone bare checkout. One
  bucket currently holds a literal matrix, an environment variable set two jobs earlier, and `uses:`
  built from `github.event`, which is an attacker choosing what runs. `abom_observation` already
  keeps both readings of a changed fact and already refuses to pick a winner between two, so the
  storage for this exists and the work is naming. Shaped so a second adapter — a Dockerized action's
  image, a JavaScript action's own dependencies, a shell script a composite action calls — is
  addable; those implementations are not in this plan. [Container
  inventory](db/container-inventory.md) owns container declarations,
  not image contents. The shared model remains unranked.
- **[A report somebody can hand to somebody else](ui/printable-report.md)** - still blocked on
  [graph layout](ui/graph-layout.md), for a different reason than before. A drawing that was
  different every run cannot be printed, and that half is fixed: a placed scope is byte-identical
  across runs. What cannot be printed now is a **161:1** canvas, and a scope wide enough to be
  worth reporting on may not be placeable at all. The bill of materials remains the other half: a
  document with no way to carry the picture; what a document covers is stated on a repository's
  row, before its download. The SVG writer it needs is built, in `internal/figure`,
  for one node's closure.

## Built, with refinements nobody has ranked

These **seventeen** plans describe workstreams that are **built and carrying real data**, and each
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
| [api/query-simulation.md](api/query-simulation.md) | checking a comparison once a pack is live, which waits with pack promotion; separating what a pack adds from what a CodeQL upgrade alone raises; enablement from the audit |
| [api/query-suites.md](api/query-suites.md) | suite staleness, and streaming result sets out of BQRS; query identity is the catalog's, which [`docs/architecture.md`](../docs/architecture.md#what-produces-the-data) describes |
| [api/reachability.md](api/reachability.md) | all-routes reach, and `needs:` ordering nothing renders |
| [db/analysis-ledger.md](db/analysis-ledger.md) | what a run records, and how a change to any of it reaches work already on record |
| [db/checkout-cache.md](db/checkout-cache.md) | flipping retention on by default, and eviction that reads the queue |
| [db/database-cache.md](db/database-cache.md) | the same two for retained CodeQL databases, and a run that must not evict its own working set |
| [db/data-efficiency.md](db/data-efficiency.md) | the standing cost review; one endpoint is still linear in rows |
| [db/declaration-provenance.md](db/declaration-provenance.md) | how a declaration arrived, and what contradicts it |
| [db/observations.md](db/observations.md) | the fact table's keys and its readers |
| [db/owned-scope.md](db/owned-scope.md) | ownership edges the declaration does not yet settle: per-account access in the pre-flight, a browser gate that never draws the declaration picker, and a failed declaration read shown as nothing declared |
| [db/pruning.md](db/pruning.md) | tombstones, compaction and what a delta reports |
| [db/ref-provenance.md](db/ref-provenance.md) | requested refs, governing locks and analyzed commits through the ledger |
| [db/supply-chain.md](db/supply-chain.md) | classification over the seed-reachable closure |
| [ui/closure-figure.md](ui/closure-figure.md) | the Explorer's terms on a details page's figure, and precomputing figures if drawing them on arrival stops being cheap |
| [ui/theme.md](ui/theme.md) | whether a color still names the same thing after a theme toggle |
| [ui/tier-suites.md](ui/tier-suites.md) | per-tier suite binding, its precedence, and cost guidance for a tier's re-analysis |

## Gaps with a decision to make

- **Enabling default setup has never run live on a test target.** Adding `actions` has: a batch on
  has-ghas/test-repo-2, under the enforced "GitHub recommended" configuration, sent one write and
  read back every earlier language plus `actions`. Every test repository sits under that
  configuration, and GitHub refuses a change to default setup's state there with a 422, so none can
  be turned off to exercise enable. Detaching one needs `write:org`, which the operator's token does
  not carry. The same blocker leaves open what GitHub detects where the only Actions code is
  `.github/actions/**` or a root `action.yml`; the classification refuses to write either way. **The
  decision is whether to detach a test repository for it, or to let the first real enable, which is
  read back like every write, be its verification.**
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
  that the next dimension does not need another one** — `Store.Reobserve`, selecting on whether an
  edges run kept sighting content, is already a third. The recorded `mode`, and the query and rule
  versions every evaluation now records, must also be selectable. See
  [`db/analysis-ledger.md`](db/analysis-ledger.md), which holds both as pending items. **It is here
  rather than in the ranking because the first is a fork nobody has taken**, and it is ranked the
  moment one is — the requeue shape is ordinary work after that, and 63 edges are already waiting
  on it.
- **The ownership gate is inert on an existing install until somebody refreshes.** A declaration
  roots nothing unless a recorded token scope says the membership list could have been complete, and
  a corpus verified before the `scopes` column carries NULL everywhere, so nothing can be
  contradicted. Conservative by design. **The decision is whether the server prompts, refreshes on
  start, or leaves it alone.**
- **A finding nobody attributed is invisible to a policy.** The evaluation runs through
  `abom_finding_node`, so a finding no node carries cannot be refused by a gate. On the crawled
  organization **520 findings across 51 repositories are linked to no node**. **The cause is now reproducible rather than counted**
  (`internal/ingest/marker_test.go`, on CLI-generated fixtures): a composite action that names no
  `uses:` is **not a node at all**, because the graph models what a `uses:` line touches — so a
  finding reported on it attaches to nothing. The same fixture shows the second half: a finding on
  a job lands on its *workflow*, because `store.LinkFindingsToNodes` matches by `path` and only its
  first pass matches a line. **The decision is whether attribution should be repaired — which needs
  a line range on the node, a schema change — or whether an unattributable finding is a permanent
  class the gate names and steps around.** A crawl seeded with the experimental marker is the right
  thing to re-measure against: the marker reports on every job and composite action, so the share it
  fails to attach sizes the gap directly rather than by inference. Take the number once a round has
  finished, because attribution runs at the end of one. **A closure makes this gap the one that
  matters most**: a cross-boundary injection lands in the callee's `run:`, and on the staged
  fixture (`testdata/closure`) both such sinks — a composite of `run:` steps and a
  reusable workflow with no `uses:` — attach to no node. On the crawled organization the closures
  added no finding, so the gap's size did not move: the 6 closure findings attached to nothing were
  already unattached regularly.
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
- **`abom_surface` is a read model nothing writes to.** It declares `permissions`,
  `permissions_scope`, `triggers`, `runs_on`, `uses_secrets_inherit`, `is_privileged` and
  `is_externally_trigger` — note the truncated column name — and holds **0 rows** on the crawled
  organization, whose `abom_observation` carries all seven facts, 54,465 rows of them, keyed to jobs. Anything
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
