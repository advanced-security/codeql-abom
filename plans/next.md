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

1. **[Make the browser suite blocking](ui/testing.md)** — `make e2e` exists and passes, and is in
   report mode on purpose because the gate turns on last. It joins `verify` once the Chromium
   download is dependable and the suite has run clean repeatedly rather than once. The same entry
   carries the populated-corpus assertions, which now have a corpus to point `ABOM_E2E_DB` at.
2. **[A business unit nothing can yet filter by](db/business-units.md)** — the dividing line is read
   from the organization, stored and reported: **7 distinct `business_id` values over 25
   repositories**, reconciling exactly with GitHub's own figures. What is missing is a predicate
   term, and it has to land **with an executor** — a term the spec parses and no query applies
   returns unfiltered results to somebody who believes they filtered. The screens follow it.
3. **[Only one read still scales with the organization](api/org-enumeration.md)** — the listing is
   bounded by what moved and **the per-repository CodeQL probe is not**: there is no code-scanning
   field on `Repository` in the GraphQL schema, checked by introspection, so it is one REST request
   each. At twenty thousand repositories that is now the entire bill. The same entry owes the
   interface the state the API already carries — which route answered, when the account was last
   listed in full, when last swept.
4. **[A gate that blocks, and can say whose rule it was](api/verdict-gate.md)** — ABOM holds the
   only transitive Actions graph, so it is the only thing that can refuse a pull request on that
   basis. **The policy half is built** — drafted, simulated, adopted, waived and auditable — so what
   remains is the base-versus-head closure diff, the verdict endpoint and the check run, over
   `store.EvaluatePolicy` rather than a second engine. Its client is deferred in
   [`api/abom-check.md`](api/abom-check.md).
5. **[The purl names a version Dependabot can match](db/version-resolution.md)** — the submitted
   purl carries a version for most commit-pinned dependencies ABOM submits, with no request to
   GitHub. The open half is the direct commit-pinned entries that are not submitted at all, and
   closing that is not obviously safe. Both the size of that set and the tags API for the pins the
   queue cannot reach are owed a measurement against a crawled organization.
6. **[A finding that stops being reported is never removed](db/finding-lifecycle.md)** — sightings
   are recorded and the sweep is written, but nothing has been sighted, so the sweep correctly
   condemns nothing and the report says nothing. The tombstone and the `_live` view wait on a crawl
   that has run the findings suites, which the edges-only crawl has not.
7. **[Code quality, measured by our own tool](tools/code-quality.md)** — the gate already holds new
   findings at zero in all four languages. What is left is complexity: **50 production functions
   over 10**, and `runServe` is the one that keeps tripping the limit when anything is added to it.
   Measured from source, so unaffected by the corpus retirement. Cleanup rather than risk
   reduction, which is why it sits here.
8. **[The coverage strip orphans a cell](ui/theme.md)** — with nothing declared the strip renders
   four items into a three-column grid, so the fourth sits alone on a second row on every page.
   Confirmed in a browser, and held by a `test.fail()` that will break the run when it is fixed.

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
