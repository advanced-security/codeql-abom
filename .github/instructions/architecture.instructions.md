---
description: "Use when changing Go code under cmd/ or internal/ in codeql-abom: the layering, and the architectural invariants that produce data which looks right and is wrong when broken. Covers verdicts coming only from rule IDs, advisories enriching rather than adjudicating, seed versus upstream repositories, ownership versus what already has a database, direct versus transitive exposure, severity x reach, saying what is not known, absent versus zero measurements, writing to GitHub's APIs being irreversible and replacing rather than adding, append-only graph tables, and queue states a reader distinguishes."
applyTo: "{cmd,internal}/**/*.go"
---

# codeql-abom architecture

Builds an Actions Bill of Materials for a GitHub organization from CodeQL results. Go 1.25,
single binary, SQLite store. Layering: `store` (SQL) → `graph`/`ingest`/`expand` (derivation) →
`serve`/`cmd` (delivery). Only `store` writes SQL.

## Architectural invariants

Breaking one of these produces data that looks right and is wrong. Each was learned from a real
bug in this repository.

**CodeQL rule IDs are the only source of a security verdict.** ABOM records and attributes
findings; it never derives its own. There is no `pin_status` column and there must never be one
— whether a reference is safely pinned is `actions/unpinned-tag`'s call. `TestSchemaHasNoVerdictColumns`
guards this. Every finding carries its `rule_id`; every claim traces to a query.

**Advisories enrich, they never adjudicate.** A GHSA record is stored as an attributed external
statement, exactly as a CodeQL result is attributed to a rule. Never derive "this action is
vulnerable" from an advisory alone — the observed reference may sit outside the vulnerable
range. Report the advisory, its range and the observed refs *verbatim*, side by side.
This matters because `actions/vulnerable-action` is exact-match only, against a curated
541-entry list, and found nothing across 204 real repositories. Most usage is floating tags no
exact-match model can resolve. The advisory layer covers that gap by declining to judge.

**Seed and upstream repositories are different.** `repository.origin` is `seed` or `upstream`
(fetched only to resolve what seeds depend on). Upstream repos contribute *edges* — a composite
action's own steps are its callers' supply chain — but never count as *dependents*. Conflating
them inflated blast radius 202 → 209 and made `actions/checkout` depend on itself.
`InsertUpstreamRepository` never demotes an existing seed: `actions/cache` is genuinely both.
`origin` is data provenance and nothing more — it is **not** a claim of ownership and must never
surface as one. Ownership is `abom_owned_scope`, and **a row in it is not yet evidence that the
operator declared anything**: the table records `declared_at` and nothing about who or how, which
is how a corpus came to assert this operator owns `facebook` and `microsoft`. Treat a declaration
as a claim needing provenance, not as a fact. See `plans/db/declaration-provenance.md`.

**What you own and what already has a database are different questions.** `has_database` says
somebody enabled code scanning — a fact about how a repository is configured, not about who owns
it. ABOM builds its own actions databases from a sparse checkout, so nothing may gate analysis on
it: it is provenance (a build worth skipping), never eligibility. The two sit in one
`catalog.Source` and one `discover.Run` pass only because this corpus was seeded through
`gh mrva`, which could see nothing else. Where a cheap question shares a code path with an
expensive one, the cheap one must stay callable alone.

**Direct and transitive exposure are different work.** `hops = 1` means the repository names the
action itself and can fix it. `hops > 1` means it arrives via an intermediary someone else may
own. Never collapse them into one "affected" count.

**Priority is severity × reach, and nothing else.** CVSS and EPSS are reported alongside but
stay out of the arithmetic, so the number remains explainable. Never blend scores.

**Say what is not known.** Coverage, pending frontier items and unanalyzed dependencies are
first-class output, not omissions.

**An absent measurement is not a measurement of zero.** Reach is recomputed wholesale, so a node
ingested since the last rebuild has no `abom_blast_radius` row — and neither does a node nothing
references, whose reach genuinely is zero. Those are different facts and the watermark in
`abom_graph_generation` is what separates them. Drawing an unmeasured node at the smallest size
asserts "nothing depends on this" in a view where size means reach. Same rule as the pinning
split refusing to print `0 yours` with no verified identity.

**Writing to GitHub is irreversible, replaces rather than adds, and belongs to a test target.**
ABOM's first external write is the dependency submission API, and it obeys none of the intuitions
a read does. A submitted snapshot **outranks GitHub's own static analysis**, so a partial one does
not enrich a repository's dependency graph — it *deletes* what GitHub correctly found. Measured on
2026-09-08: a snapshot naming one of three direct dependencies removed the other two from a live
repository, and they returned only after a complete resubmission. Precedence keys on
`file.source_location`, so a deliberately distinct manifest name buys no coexistence — anything
naming a real workflow path owns that path's whole dependency list. There is **no delete
endpoint**; the only repair is a superseding snapshot. So: submission targets a repository created
for the purpose (`advanced-security/codeql-abom-test1`, `-test2`) until a completeness gate exists
with tests behind it, and the guard is code rather than care. This is absent-is-not-zero again, at
the one boundary where the mistake lands in somebody else's system.

**The graph tables are append-only, and only the compaction may delete.** Nothing deletes from
`abom_node`, `abom_edge` or `abom_finding` except `store.Compact`, which bumps an epoch that
forces every client to reload; only `abom_blast_radius`, `abom_exposure` and `abom_account` are
truncated. Every delta cursor the interface holds is a row-id watermark, so an *unannounced*
`DELETE` would silently hand clients an update that skips or repeats work. Removal is a tombstone
(`removed_at`) that the delta reports, and reads go through `abom_node_live` / `abom_edge_live` so
they cannot opt out of it. `TestGraphTablesAreDeletedOnlyByTheCompaction` and
`TestReadsGoThroughTheLiveViews` guard this.

**A queue state a reader distinguishes is a state the interface must have.** `pending` with
attempts spent is not the same as untouched, and a pause that takes effect between cycles is not
the same as paused. Collapsing either reads as a control that did nothing.

**Ownership is declared, and the graph is its closure.** Nothing enters the graph that the owned set
does not reach; the walk is inductive from declared roots. Four notions stay separate — `origin`
(how a row entered), in-scope (holds a seed), `abom_owned_scope` (what the operator is answerable
for) and `abom_account` (what `gh` reports). Only the third implies anyone can change anything, and
only `owner` or `admin` roles are eligible to be declared. Belonging is not being answerable.

**Only something owned can be included or excluded by a repository property.** A grouping is read
from custom properties, which are collected for owned repositories and nowhere else, because the
permission to read anybody else's does not exist. So an upstream or external node carries no
grouping and enters an answer only by being reached from an owned node that does. A node that
resolves to no repository is *ungrouped* — never the organization's default value, and never the
repository that happened to observe it.

**A property filter names sources, and the answer is their closure.** Exclusion is taken over the
sources and the closure is computed afterward, so a repository one group owns and another group
depends on survives its owner's exclusion. Removing it would answer "what does this group depend on"
by deleting part of the answer, which is the failure that made the filter worth applying to a graph
rather than refusing on it.

**A route declaring a term is not evidence every branch applies it.** `/api/exposure` answers by
repository or by advisory depending on `by`; the route declared `properties`, the handler parsed it
before the switch, and only one arm used it — so the other returned the whole organization under a
predicate that said otherwise. Refusing unknown parameters proves a term was read, never that it was
applied. Where a handler branches, follow the scope into every arm.

**No capability may exist only in a client.** If the interface can do it, an endpoint can do it. A
client renders; it does not decide — filtering, folding and layout are rendering, and anything that
changes what the corpus holds, or what it asserts, belongs behind the API. Guarding is a property of
the endpoint, never of the caller: a control-guarded action is guarded identically for the CLI, the
Action and the interface.

**A view selects; it never adjudicates.** The predicate language chooses what to show and gains no
`blocking` field — a verdict belongs to the gate, evaluating a policy the operator wrote. An
unsupported predicate is an error, never a silent no-op, and an empty selector matches nothing
rather than everything. **Parsing a predicate is not applying it**: six were accepted and silently
ignored, and only an end-to-end check found them.

**The policy is the operator's; ABOM only evaluates it.** No rule may be implied, defaulted or
inferred from severity alone. A verdict records both the evidence and the policy that acted on it,
because a block nobody can explain gets switched off. **Inconclusive is never silently a pass** —
fail-closed and fail-open are configured choices.

**A policy nobody simulated is not adopted**, and one evaluation answers both. Adoption records the
simulation it was adopted against, because a policy that cannot say what it was expected to block
cannot be argued about afterwards; a gate enabled on a backlog is switched off within a week.
`store.SelectorFor` is the single path from predicate to selector — two constructions of one
predicate are two policy engines, and the one nobody simulated is the one that refuses a pull
request. A simulation also reports what it **could not see**: findings the rules name that no node
carries are invisible to the evaluation, and an answer that omits them looks complete and is not.

**An exclusion is enumerated. An exception is enumerated and expires. Neither is ever a pattern.**
A pattern cannot be audited and silently covers a repository created after it was written, which is
a waiver nobody granted. Expiry is read at evaluation time, never swept, so an exception stops
applying the moment it runs out rather than when a job next runs. A waiver excuses a repository and
does not hide its findings — folding the two would make an excused repository indistinguishable
from a clean one. Every waiver, expired ones included, stays listable in full: removing one when its
time runs out erases the fact that somebody granted it.

**A person chooses every alert.** Nothing uploads by itself. The tool name and category ABOM writes
are constants, never read from a source SARIF: changing either moves every alert, which reads as a
batch of new findings and a batch of fixes that never happened.

**Conditions are reported verbatim and never evaluated.** `github.ref == 'main'` is a fact about the
workflow; whether it holds is not static analysis's to settle. Job ordering comes from `needs:`,
ingested as its own relation — never from the Actions CFG, which is an AST pre-order walk that does
not consult `needs:` and would assert an order between jobs that actually run in parallel.

**A reference is recorded as written.** `actions/checkout@v7` is evidence about what the
organization runs, even where upstream publishes no such version; nothing may normalize, correct or
truncate it on the way to a node. A reference ABOM does not understand is refused explicitly — it is
not a dependency on an account, and new syntax must never fall through to the owner/repo split.

**A version submitted on a claim is a reference nothing verified.** A pin comment is somebody's
assertion; only a measurement — the crawl, or the tags API — names a version fit to submit, and a
measurement always outranks a claim.

**Runtime and build-time exposure are different.** A dependency is runtime when some edge naming it
comes from a container inside the seed-reachable closure; otherwise only an upstream project's CI
runs it, and reporting that as exposure overstates it. Classification comes from `edge_kind`, which
CodeQL already emits, and is never re-derived from file paths.

**Queries are routed by their own metadata.** The set is resolved at runtime, never compiled in, and
each query's `@kind` decides where its results go, so renaming a file cannot re-route it. Four
shapes are accepted — edges, observations, `@kind graph`, SARIF — and anything else is refused. An
empty tier binding legitimately means "do not analyze this tier", which is why `abom_tier_configured`
exists to tell it from a tier nobody configured.

**A finding is evidence attached to a node, never a verdict about it**, and every finding names its
producer and how to reproduce it. Where a provider cannot establish a fact the state is *unknown*,
which is not permissive. A provider may not invent an edge: findings attach to the graph, and only
the crawl changes its shape.

**Requeueing keeps what is already known.** Clearing results to re-run would blank the Explorer for
as long as the analysis takes, turning a refresh into an outage.

**Reading an account is one path, whoever walks it.** A person pressing the button and the loop on
its period both go through `internal/enumerate`. A second implementation would be a second reader of
`DeclaredAccount.Covers`, which is how an includes-only declaration smuggles in the rest of its
account.

**A read that failed is not an account that shrank.** An account that could not be reached is
recorded and named, and the cycle carries on with the work already queued. Reporting the graph as
current because a listing returned nothing is the coverage lie in its purest form.

**Efficiency is never bought by narrowing what is reported without saying so.** Skipping a
repository because its validator matched is a measurement that it did not change. Skipping one
because a budget ran out is a coverage gap. The two must never render alike — a bounded read that
silently drops the rest turns a performance fix into a coverage lie.

**A 304 is evidence of sameness, not evidence of freshness.** It says the representation did not
change, which is exactly what was asked. It does not license reporting the derived graph as
re-measured, and a stored measurement time must still be the time something was actually read.

**An unknown budget is not a budget of nothing.** A rate limit nobody reported must not throttle a
crawl, and an exhausted one must not read as unlimited because the arithmetic rounded to zero.
Pacing guards against exhaustion; it is not a governor, and a budget with room must leave the crawl
alone.

**Spend is what the API charged, never a delta between two readings of what is left.** GraphQL's
`remaining` is not an instrument: three consecutive probes each reported `cost=1` while `remaining`
did not move at all. Read `rateLimit { cost }`. A figure subtracted from a counter somebody else
also decrements measures the whole account's traffic, not this crawl's.
