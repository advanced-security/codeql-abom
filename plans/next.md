# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the one-line
reason for it.

For what ABOM already does, read [`docs/architecture.md`](../docs/architecture.md). For where it is
going and why that is the destination, read [`docs/vision.md`](../docs/vision.md); for the same
thing in themes rather than tasks, [`docs/roadmap.md`](../docs/roadmap.md).

House rules:

- **Plans describe what is next, never what was done.** Git history is the record of what shipped.
  When a plan's last pending item lands, the plan file goes away; if it held a rule, the rule moves
  to `.github/instructions/**` or `docs/**` first.
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

1. **[The Explorer draws hundreds of points and names none of them](ui/explorer-legibility.md)** —
   the view this project is named after opens on **395 of 2,639 nodes** with no labels at any zoom,
   and its collapsed view folds them into 8 groups and draws two points. 2 depends on it: grouping
   that is correct and invisible is not grouping.
2. **[A scope you cannot build, name or keep](ui/scope-control.md)** — the API can be asked for
   `properties=business_id:3`, and no screen can ask. `ViewSpec.cs` does not carry the term,
   `abom_saved_view` and its three routes have no interface at all, and the one screen that writes a
   predicate to the address bar is the only one that reads one. Policies, reports and campaigns each
   start by naming a set of repositories, and today that set can be produced by hand-editing a URL
   and in no other way.
3. **[A term is enforced or refused, and today one is neither](api/filter-enforcement.md)** — every
   term is now applied by the server, applied by the client, or refused by name, and every endpoint
   that can be bounded to a repository grouping is. What is left is one thing: `abom_policy` still
   carries `scope_kind` and `scope_value`, a second narrowing mechanism evaluated by different code
   for what a predicate now says directly. **Demoted from 1**, because the half that made this
   urgent has landed and the remainder is a deduplication no reader can see, while 1 and 2 are what
   make the filter reachable by anyone who is not editing a URL by hand.
4. **[One seeded corpus, shared](ui/testing.md)** — the corpus now exists: 25 seed repositories, 50
   property rows, **1,495 findings**, 6.2 MB. What it is not is *shared*. It lives on one
   workstation, so the browser suite still gates only the empty half in CI and the populated
   assertions run nowhere anybody else can reach. It also holds **zero** dependency edges between two
   owned repositories, so one rule about excluding a repository grouping has no real data behind it.
5. **[Only one read still scales with the organization](api/org-enumeration.md)** — the listing is
   bounded by what moved and **the per-repository CodeQL probe is not**: there is no code-scanning
   field on `Repository` in the GraphQL schema, checked by introspection, so it is one REST request
   each. At twenty thousand repositories that is now the entire bill. The same entry owes the
   interface the state the API already carries — which route answered, when the account was last
   listed in full, when last swept.
6. **[GitHub already parsed the first hop, and we never compare](api/dependency-graph-crosscheck.md)**
   — our central claim is that the native Actions dependency graph stops at the first hop, and that
   claim is sourced from documentation rather than evidence. Nothing checks our CodeQL extraction
   against an independent reading of the same files either. `PackageURLs()` already reads GitHub's
   SBOM; it is pointed only at our own submissions. Ranked above the gate deliberately: a check run
   that blocks somebody's pull request should not be the first thing to discover the graph was
   wrong. Accuracy only — the seeding path is refused in the plan, because an SBOM describes a
   default branch and we analyze a pinned commit.
7. **[A policy can only say what the filter language can say](api/policy-language.md)** — its first
   item moved to 2 and its fourth is reversed there, so what remains is the half that matters: the
   language cannot say *carries a live advisory* or *is reached from something you own*, which is
   most of what an Actions rule is for. **Above the gate deliberately, because the gate enforces
   whatever this says** — and the vision now names these terms as what a rule is about.
8. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM holds the
    only transitive Actions graph, so it is the only thing that can refuse a pull request on that
    basis. **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
    remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
    `store.EvaluatePolicy` rather than a second engine. The ground under it is firmer than it was:
    exposure and the submission closure now agree about which version a route belongs to, held by
    tests, so a verdict and a submission cannot disagree about the same supply chain. Depends on 7
    for what a rule may say. Its client is deferred in [`api/abom-check.md`](api/abom-check.md).
9. **[The purl names a version Dependabot can match](db/version-resolution.md)** — the submitted
    purl carries a version for most commit-pinned dependencies ABOM submits, with no request to
    GitHub. The open half is the direct commit-pinned entries that are not submitted at all, and
    closing that is not obviously safe. The measurement it was owed is now available rather than
    hypothetical: a crawled organization with **571 recorded ref resolutions** is on disk.
10. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — unblocked.
    The sweep was written against a corpus where nothing had ever been sighted, so it correctly
    condemned nothing and proved nothing. There are now **1,724 sighting rows** from a crawl that ran
    the findings suites, so the tombstone and the `_live` view can finally be exercised against a
    finding that really does stop being reported.
11. **[Remediation summarizes two things into their more reassuring halves](ui/remediation-honesty.md)**
    — the strip counts **11 live advisories** and the table shows **3**, with a *critical* among the
    absent and nothing on screen saying why; and `Fix: available` is one boolean over an advisory
    whose ranges include **no fix published**, with 5 repositories sitting in exactly that range.
    Both exclusions are correct underneath; what is wrong is that a reader is told the comfortable
    half.
12. **[Reach counts a repository that pins a version without the dependency](db/reach-version-constraint.md)**
    — exposure and the submission closure now agree about which version a route belongs to; reach
    carries none of it, and reach is what orders the crawl. **132 of 225** dependencies are analyzed
    at more than one reference. It sits below the interface entries because it is invisible to
    everyone and its first step is a decision that may legitimately conclude no change.
13. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate already holds new
    findings at zero in all four languages. What is left is complexity: **50 production functions
    over 10**, and `runServe` is the one that keeps tripping the limit when anything is added to it.
    Measured from source, so unaffected by the corpus retirement. Cleanup rather than risk
    reduction, which is why it sits here.
14. **[The coverage strip orphans a cell](ui/theme.md)** — with nothing declared the strip renders
    four items into a three-column grid, so the fourth sits alone on a second row on every page.
    Only the 4 and 5 item cases are affected; a populated corpus renders 6 and divides cleanly,
    which is why this is invisible in exactly the runs most likely to be looked at. Held by a
    `test.fail()` that will break the run when it is fixed.

The filter block held the top slot on the grounds that scoping to a business unit or an application
is what a policy, a report and a remediation campaign each start with. That argument has been
honored rather than abandoned: the server half is built, so the block now sits at 3 holding only a
schema deduplication, and the two interface entries that make the filter usable by somebody who is
not editing a URL by hand have taken its place. Said plainly, because this file has demoted things
silently before. Disagreeing with it is the point of writing it down.

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
- **[A finding has to reach somewhere a person can fix it](api/code-scanning-upload.md)** — required
  eventually, since findings currently terminate on our own screen. Gated on a rule, not on the
  upload: **no alert may exist that a person did not choose**.
- **[Read in response to events, not on a timer](api/event-driven-reading.md)** — the detection
  works; only the trigger is missing, and the address it would arrive at is
  [`api/org-server.md`](api/org-server.md).
- **[Findings are a pluggable class](db/finding-providers.md)** — the seam is built and carries real
  data; a second provider is deferred by decision.
- **[Every action has an API](api/api-first-invariant.md)** — already true, and not yet provable.
  Upkeep, with no client that needs it until the gate does.
- **[Collapse `name@ref` back to one node](ui/explorer-references.md)** — per-reference nodes are the
  useful default and the only behavior. The counts for the other reading already ride on every node.
- **[Publish packs to GHCR](ql/packs.md)** — deliberate. The naming and versioning landed early so
  that publishing is never the thing blocked.
- **[Does a transitive alert open a pull request?](api/dependency-submission.md)** — the last link in
  the remediation chain is an inference from GitHub's documentation, and `codeql-abom-test1` is where
  to measure it. Not to be softened in prose.

## Built, with refinements nobody has ranked

These fifteen plans describe workstreams that are **built and carrying real data**, and each keeps a
short list of refinements — 43 items between them. They are not ranked because none of them is the
next thing to build; they are the backlog a workstream leaves behind, and a reader asking "what
next" should not have to read past them. They are listed because a plan in neither the ranking nor
*Deferred* is invisible, and all fifteen were, which is how an index stops being one.

Promote from here when a refinement stops being polish — when it blocks something ranked, or when a
measurement makes it urgent.

| Plan | What its refinements are about |
| --- | --- |
| [api/advisories.md](api/advisories.md) | refresh cadence and what an age ceiling should cost |
| [api/query-suites.md](api/query-suites.md) | suite selection, and streaming result sets out of BQRS |
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
| [ui/closure-first-explorer.md](ui/closure-first-explorer.md) | what the Explorer opens on |
| [ui/discovery-config.md](ui/discovery-config.md) | the run controls as one box |
| [ui/tier-suites.md](ui/tier-suites.md) | per-tier suite binding and its precedence |

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
  reference corpus: `facebook/install-dotslash` holds **15 `actions/*` findings, none linked**,
  which is the difference between a simulation reporting 40 repositories and the findings table
  saying 41. A simulation now reports the gap rather than hiding it. **The decision is whether
  attribution should be repaired, or whether an unattributable finding is a permanent class the
  gate names and steps around.**
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
- **Three places show a reader a number they cannot reconcile.** The Discovery queue table sorts by
  score while the crawl claims by depth, so the order shown is not the order that will happen.
  Discovery's tier chips and its objective panel decompose one queue two ways that do not sum alike,
  with nothing saying why. And the Bill of Materials states how many findings lack a run key, hash or
  digest without letting a reader see which run produced any given finding.
  **A fourth:** `expand-all` ends with "N repositories", and N is the number of analyses — a crawl
  reporting 319 left 101 rows in `repository` and 317 in `abom_query_run`. The word is wrong rather
  than the count.
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
