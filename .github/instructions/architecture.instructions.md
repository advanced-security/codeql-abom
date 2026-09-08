---
description: "Use when changing Go code under cmd/ or internal/ in codeql-abom: the layering, and the architectural invariants that produce data which looks right and is wrong when broken. Covers verdicts coming only from rule IDs, advisories enriching rather than adjudicating, seed versus upstream repositories, ownership versus what already has a database, direct versus transitive exposure, severity x reach, saying what is not known, absent versus zero measurements, append-only graph tables, and queue states a reader distinguishes."
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
