# What was analyzed, when, and which version of it

**Status:** shipped. The queue is keyed by dependency *and reference*, checkouts fetch the
reference asked for, and coverage is measured against the versions actually in use. What that
exposed: **574 of 3,298** versions in use have been analyzed. Suite provenance per row (part 2) is
also shipped, and reports *not recorded* for **1,099 of 1,149** analyzed rows — pack identity
began being written part-way through this corpus, so most rows predate it.

## The complaint

The Discovery page configures *which query suites run on which tier* and then lists dependencies
with no way to tell what any row was analyzed with, when, or which version of it. The
configuration is a promise about the next round; the table is the residue of previous rounds;
nothing joins them.

## What reconciling changed

Every premise below was checked against the schema, the corpus and the code before being written
down, because the stub asserted three gaps and two of them were already partly closed.

| Stub said | Actually |
| --- | --- |
| No last-analyzed time is recorded | `abom_frontier.analyzed_at` exists and is **100% populated** (1,141 of 1,141 done rows) |
| The resolved commit has no route out | `FrontierItem.ResolvedSHA` is already in the API **and already rendered** as the "Analyzed commit" column |
| No per-row analysis history | An "Analyses" column already renders `analysis_count` |
| `name@ref` is possibly a display change | It is not. Nothing is analyzed at a ref anyone uses |

The columns that "do not exist" are shown only when `MixedStates || _status == "done"`. Filtering
the queue to `status=queued` hides them, which is correct — a queued row has no commit — and is
why they were reported missing.

**So the honest gap is narrower than the complaint, and one part is far worse.**

## 1. Last analyzed time — small, and genuinely missing

`analyzed_at` is in the table and in no Go struct. `FrontierItem` carries `ResolvedSHA`,
`AnalysisCount`, `ClaimedAt` and `NextAttemptAt`, but not `analyzed_at`, so it reaches neither the
API nor the grid. One column through `store` → `serve` → `Models.cs` → `Discovery.razor`, beside
the "Analyzed commit" column it belongs with.

Worth stating for the same reason coverage is stated: a fresh row and a year-old row are different
evidence, and "re-analyze this tier" replaces an age distribution the operator cannot currently
see.

## 2. Which suites produced this row — medium, and needs a join that does not exist

`abom_query_run` records `pack`, `pack_version`, `pack_digest`, `cli_version`, `query_id`,
`db_commit` and `ran_at`. On the corpus: **1,707 runs across 1,097 repositories, 2 distinct
packs**, and **1,299 of 1,707 carry `db_commit`** — the remaining 408 predate run identity and
have no commit to join on.

**Reconciling later found the worse number.** Carrying a commit is not the same as carrying a
pack: **1,555 of 1,707 runs record no pack at all**, so the join succeeds and returns nothing to
show. Counted from the page rather than the run table, only **50 of 1,149** analyzed rows can
name what analyzed them. See *What suite provenance measured*.

There is no foreign key from `abom_frontier` to `abom_query_run`. The join has to be
`repository` + `db_commit` against the frontier row's owner/repo and `resolved_sha`, and it will
miss those 408.

**Constraints this must satisfy:**

- **Attribution, not derivation.** The suite shown against a row comes from the recorded run,
  never from the tier's current binding. The binding is what happens *next*; the row is what
  already happened. Showing the binding would make a just-enabled suite look retroactively applied
  — the same error class as deriving a verdict from an advisory instead of a rule.
- **Absent is not "none".** Those 408 runs, and any row analyzed before run identity landed, have
  no pack to show. That is *not known*, and must not render as "no suites" — the same rule that
  stops an unmeasured node being drawn at the smallest radius.
- A row analyzed under `abom-dependency-graph` alone has no findings **because none were looked
  for**. Today that is drawn identically to a row that was searched and came back clean. Those are
  opposite statements.

Open question: per-row *suites*, or per-row *divergence from the current binding*? The second is
far less to render and answers "is this row stale" directly, which is probably the real question.

## 3. `name@ref` — the one that is not a UI problem

**Every dependency is analyzed at its default-branch tip.** `checkout.go` clones with
`--depth=1` and no ref, then records `Ref: "HEAD"`. The corpus agrees: `analyzed_ref` is `HEAD`
for **1,213 of 1,213** rows.

Meanwhile the ref anyone actually uses is an *edge* attribute — `abom_edge.ref_raw`, `ref_value`,
`pin_comment` — and `abom_node` has no ref column at all. Nodes are identity-by-name.

| | |
| --- | --- |
| external callees carrying a ref | **1,262** |
| used at more than one ref | **438** (35%) |
| distinct `name@ref` pairs in use | **2,700** |
| frontier rows analyzed | 1,141, every one at HEAD |
| worst | `actions/checkout` at **51** distinct refs; `github/codeql-action/*` at 43–44 |

So the interface shows one row per action, analyzed at a commit **no repository necessarily
uses**, while the organization runs 2,700 distinct versions. The existing tooltip already half
admits this — *"The commit this was analyzed at. The reference may point somewhere else by now"* —
but the drift it describes is not drift. The analyzed commit was never the used one.

**Why this is a correctness problem and not a nicety.** A finding is recorded against the action
and attributed to a CodeQL rule, which is sound. But presenting it beside an organization's usage
invites the reading "you are exposed to this", and that inference is not supported when the
analyzed tree is HEAD and the repository pins `@v3`. This is exactly the line the advisory layer
already refuses to cross: report the observed refs and the analyzed ref *side by side*, and let
nobody derive a verdict the analysis does not carry.

**This is the decision the plan exists to force**, and it is a fork, not a detail:

- **(a) Report the mismatch, analyze nothing new.** Cheapest and immediately honest: show the
  analyzed ref beside the refs in use, and count how many refs a finding does *not* cover. No
  migration, no extra crawling. It makes the limitation legible instead of fixing it.
- **(b) Analyze per `name@ref`.** Correct, and a different system: the frontier is keyed on
  `node_key`, so this changes the unit of work, the queue's primary key, the dedupe, the scoring
  and the recheck loop. 2,700 units instead of 1,141 is roughly 2.4x the crawl, and that is only
  the refs seen so far.
- **(c) Analyze the most-used ref per action.** A middle that sounds reasonable and needs care: it
  makes coverage depend on popularity, and would have to say plainly which refs it did not cover
  rather than implying the one it picked stands for all of them.

**Recommendation: (a) first, and let its numbers decide between (b) and (c).** Until the mismatch
is measured per finding, the argument for 2.4x the crawl is speculation. (a) is also the only one
of the three that cannot make the data less true.

## The count that empties itself

Pressing **Re-analyze** on a tier drops that tier's count to `0` until the work is redone, because
`TierCounts` counted `status = 'done'`. That is right for the button — a requeue only affects
`done` rows, so it is exactly the cost about to be spent — and was wrong for the column header,
which reads as membership and asserted that a tier holding sixteen dependencies had none.

**Fixed.** `TierCounts` returns a `TierCount{Total, Analyzed}`: the header states the tier's size,
the button states what re-running costs, and a requeue empties the second while leaving the first
alone.

## Sequencing

1. ~~`analyzed_at` to the grid.~~ **Shipped.**
2. ~~Tier membership alongside tier analyzed.~~ **Shipped**, along with a row that names its tier.
3. ~~Analyzed ref against refs in use.~~ **Shipped, and it went past (a) to (b)** — the queue
   analyzes each reference rather than reporting a mismatch it declines to fix.
4. ~~Suite provenance per row.~~ **Shipped**, and it mostly reports *not recorded* — see below.

## What suite provenance measured

**The stub's number was the wrong one, and the real gap is four times worse.** The entry quoted
"408 of 1,707 runs predate `db_commit`", which is arithmetically right and answers a question
nobody asks. Measured from the side that matters — the rows on the page:

| | at first measurement |
| --- | --- |
| runs recorded | **1,707** |
| runs carrying no pack at all | **1,555** (91%) |
| analyzed rows that can name their suites | **50 of 1,149** (4.4%) |
| analyzed rows that answer *not recorded* | **1,099** |

**These are point-in-time figures, and the gap closes by itself.** Re-measured about an hour
later with a crawl running, having changed no code: runs **1,707 → 1,783**, runs carrying a pack
**152 → 228**, rows naming their suites **50 → 105**. The prediction that provenance fills in as
re-analysis proceeds is now observed rather than argued, and it removes the need for any
backfill: the only rows that stay unrecorded are ones nothing re-analyzes.

So the column ships nearly empty, and that is the honest result rather than a reason not to ship
it. `pack`, `pack_version` and `cli_version` began being written part-way through the corpus:
every run before `15:50` has none and every run after `20:01` has all three, a clean cutover with
nothing straddling it. The gap is historical, not a live defect, and it closes as re-analysis
proceeds — which is exactly what the per-reference requeue is about to cause anyway.

The join is `repository` + `db_commit` against the row's `resolved_sha`, and its output was
checked against the same set computed independently in SQL: **the rows flagged as knowing their
suites are exactly the SQL-knowable set intersected with the page, with no false positives.**

**Two guards are load-bearing, and both were written as tests first.** Most queue rows have no
`resolved_sha` and 408 runs have no `db_commit`, so without the empty-string guards the join
degenerates into every commitless run matching every unanalyzed row — a *queued* dependency would
have reported suites it never saw. And a run against a different checkout of the same repository
is a different analysis, so the commit is part of the key rather than a decoration on it.

**What was nearly missed: the read path had no index.** The join is a correlated subquery per
row, and the plan showed `SCAN r` — a full pass of the run table, twice per row, 500 rows to a
page. It cost **198 ms** against **9.7 ms** indexed, and the run table grows with every analysis,
so it would have degraded silently rather than visibly. Isolated from the rest of the endpoint,
provenance costs **0.29 ms** a page indexed (0.96 → 1.25 ms) against roughly 190 ms without.

That index also had to be created somewhere new. `CreateIndexes` runs after bulk loading and a
read-only server never calls it, so the index would never have existed on the machine that needs
it. `TestIndexesAreNotCreatedByMigrate` asserted a categorical ban, but its stated reason is the
cost of maintaining indexes *during a million-row insert*; it now names the bulk-loaded tables it
is actually protecting, and the run table — thousands of rows, read on every page load — is
indexed on open.

## What per-reference identity measured

Option (a) was to report the mismatch; the work went to (b), analyzing each reference, because
reporting a gap the tool refuses to close is only half an answer. The queue is now keyed
`(node_key, ref)`:

| | |
| --- | --- |
| distinct `name@ref` pairs in use | **3,298** |
| analyzed | **574** (17%) |
| queued | 30 |
| **never queued** | **2,668** (81%) |

The 574 are exactly the pairs whose `uses:` names no reference at all — the only ones a
default-branch analysis legitimately covers. Everything else was reported from a reading of a tree
nobody runs, while the interface said 1,149 dependencies analyzed and looked complete.

The migration rebuilds the table, because SQLite cannot alter a primary key. On the real corpus:
**1,253 rows in, 1,253 out**, `done` 1,149 → 1,149, `resolved_sha` 1,213 → 1,213, and existing rows
keep `ref = ''` — empty rather than `HEAD`, because inventing a reference name they never recorded
would be a claim rather than a migration.

**The dangerous part was the queue addressing rows by name.** Every `WHERE node_key = ?` was
correct while one dependency meant one row, and silently wrong the moment it meant several — one
completed reference would have marked every reference done. `frontier.Key` makes the pair the only
way to address a row, so the compiler refuses the old form rather than a reviewer having to catch
it. The same applied to the work itself: `groupByRepository` collapsed on `owner/repo`, which would
have analyzed one tree and attributed it to every reference, and each reference now gets its own
directory because two checkouts of one repository would otherwise overwrite each other mid-round.

**A scalar subquery returns an arbitrary row, not an error.** `frontierState` and the depth lookup
in `graph_full.go` each read one frontier row per node; with several they would have picked
whichever SQLite reached first, and the Explorer would have drawn a node's state from a coin toss.
They now aggregate, and the states rank so the least settled wins: a node with four analyzed
references and one still queued is *not* analyzed, which is the coverage gap rather than a
rounding decision.

## What the tier work measured

**Every tier header had been understating itself**, because it counted `done` rows and called them
"dependencies":

| tier | was shown | holds |
| --- | --- | --- |
| owned | 9 | **16** |
| direct | 458 | **518** |
| transitive | 682 | **719** |

The totals now sum to **1,253**, the whole queue, where the old numbers summed to 1,149 — the done
rows. 104 dependencies sat in tiers that declined to count them.

**The `Depth` column was hiding the most actionable fact on the page.** It was reported as useless
because every row in the default view reads `1`. Measurement agreed and then found something
worse. Depth does vary across the corpus — 527 / 458 / 166 / 82 / 20 across depths 1 to 5 — but
score and directness correlate, so **all top 20 rows by score are depth 1** and the column is
constant exactly where it is read.

The worse part: **owned dependencies sit at depths 1, 2 and 3 — 9, 3 and 4 of them** — so seven of
the sixteen the operator can change themselves were drawn as depth 2 or 3, indistinguishable from
an intermediary somebody else owns. *Owned is not a depth range*, which this repository's own tier
definition states, so no hop count can express it. The column now names the tier and keeps the
hops in the tooltip, where they explain the tier rather than standing in for it.

`analyzed_at` was populated for **1,149 of 1,149** analyzed rows and reached no Go struct, so the
age of every result was on disk and nowhere else. `FrontierSnapshot` derives the row's tier from
the same `tierOfRow` expression the counts use, so a row and the header above it cannot disagree.

## Verification

Anything built from this needs the real corpus, not fixtures: the 2,700 figure, the 438 multi-ref
actions and the 408 runs with no `db_commit` are all properties of a real crawl, and each is a
case a fixture would have to be *designed* to contain — which is how the analysis-identity gap
survived this long.
