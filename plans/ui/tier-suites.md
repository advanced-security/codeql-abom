# Per-tier suite controls

**Status:** re-analysis and per-tier suite selection both shipped; cost guidance is still a count
rather than a duration. The `owned` tier, hiding the self-check suite, and a queue colour that is
a colour all shipped and are measured below.

## Why this exists

The set of queries ABOM runs is meant to keep growing, and not every tier deserves the same set.
A repository under review is worth a broad pass; something reached through three intermediaries
is worth the minimum needed to place it in the graph. Today that choice is a single
`--expand-suite` flag applied to everything, so the trade-off is made once at the command line
and is invisible to whoever reads the result.

The interface has to state the cost, because pressing one of these buttons spends minutes of
cloning and CodeQL database building that nothing can take back.

## Done

Re-analysis by tier: **Re-analyze direct (522)** and **Re-analyze transitive (691)**, each
carrying its count on the control and its consequence in a tooltip. `POST
/api/discovery/requeue/{tier}` is guarded exactly like Start, for the same reason.

Tier is derived from depth (`direct` = depth ≤ 1) rather than stored, because depth is already
maintained as the shortest route from the repositories under review and a second copy could
disagree with it.

**Per-tier suite selection.** A grid on the Discovery page, one row per suite and one column per
tier, each column headed by how many dependencies the tier **holds** and, beneath it, how many are
currently analyzed. It headed only the analyzed count until re-analyzing a tier made it read as
empty; see `db/analysis-identity.md` for the numbers.
suites offered are read from disk at request time — `runner.AvailableSuites` over the packs under
`ql/`, returned as `<pack>:codeql-suites/<name>.qls` — so a suite appears by being dropped in,
with no release.
Anything already bound stays offered even when it is not on disk, because a binding may name a
suite from an installed pack and dropping it from the list would make it look invented.

A change takes effect on the next round, never the current one: a round resolves its queries once
before any repository is claimed. It reaches work already analyzed only when that tier is
re-analyzed, which is what the buttons beside it are for.

## What offering every suite on disk turned up

The corpus check showed three suites offered, and the third is `abom-selfcheck.qls` — the gate
this repository runs against its *own* workflows. Filtering it out by name was the obvious fix
and is the wrong one: a compiled-in exception list is exactly the release-coupled hard-coding
this control exists to remove, and the next suite someone adds would face the same question.

It does expose a sharper edge, though. `abom-selfcheck.qls` is findings-shaped, and the BQRS lane
drops findings-shaped queries silently because the SARIF lane does not exist yet. So a tier bound
to it would look configured and analyze nothing. That is not a fault of this control — the
`direct` default already binds `abom-findings.qls` and has the same problem — but it is the
second place the missing lane now shows through, which is why it moved up the ranking.

**Both halves of that have since expired.** The findings lane shipped, so a tier bound to
`abom-selfcheck.qls` now analyzes normally. And the pack restructure removed the objection to
hiding it: when the paragraph above was written all three suites lived in one pack, so "exclude
self-check" could only mean a suite name. Self-check is now its own pack,
`advanced-security/abom-actions-selfcheck-src`, holding nothing else — so the exclusion is a
property of one pack that exists only for CI, not a list that grows. A new suite dropped into the
graph or findings packs is still offered with no release, which is what the objection was
protecting.

## Hiding the self-check suite

`make selfcheck` names the suite directly with `--suite`, so removing it from what is *offered*
cannot break it.

**Filter server-side, not in Blazor.** A suite the API advertises is a suite something can bind;
hiding it only in the interface leaves the binding reachable and the grid lying about what exists.

**A suite already bound must still be shown** — the rule this plan already states for a suite that
is not on disk, for the same reason: one that runs and cannot be seen is worse than one that is
merely irrelevant.

## An Owned tier, ahead of Direct

Tier is derived from depth, which this plan records as deliberate. **Owned is not a depth
property.** A dependency you own sits at any depth: `octo-org/app` naming `octo-org/shared-action`
is depth 1, and reaching the same action through someone else's is depth 3. It is an ownership
property, and the store already answers it — `OwnedSet()` returns the verified accounts,
lower-cased, because GitHub treats account names case-insensitively.

Tier selection therefore stops being a function of depth alone and becomes precedence:

| Tier | Membership |
| --- | --- |
| `owned` | `owner` is a verified account |
| `direct` | otherwise, `depth <= 1` |
| `transitive` | otherwise, `depth > 1` |

Precedence has to be stated because the categories overlap and the columns are exclusive: a
dependency counted twice makes the three counts exceed the queue. On the reference corpus the
split moves **448 / 689 to 16 / 439 / 682** — 13 owned items from `github`, 2 from
`GitHubSecurityLab`, 1 from `advanced-security`.

**Why it earns a tier.** The reader's leverage is completely different. An owned action is one
they can change today; a transitive one arrives through an intermediary someone else maintains.
It is the tier where custom or additional queries actually pay.

**It must not change what runs.** Seed `owned` from whatever `direct` is bound to at migration
time, so behaviour is identical until the operator changes it. The knock-on belongs in the record
rather than being discovered: once the bindings differ, a reclassified dependency gets a different
query set, a different run identity, and therefore one re-analysis. That is the skip working.

**With no verified accounts there is no Owned column.** `abom_account` is empty until the operator
verifies, and `Verified()` exists precisely so that with none "every figure is undivided and the
interface must read exactly as it did before the feature existed". A column reading **0
dependencies** would assert the operator owns none, which nothing established — the same refusal
as the pinning split declining to print `0 yours` without a verified identity.

`tierWhere` feeds both `RequeueTier` and `TierCounts`, so the precedence lives in one SQL fragment
or it drifts between the count and the requeue. `tierOf` in `internal/expand` needs the owned set
too, read once per round.

## Queued is yellow, and colour may be reclaimed between pages

Dark mode is greyscale by construction — every surface, border and disabled control is a grey — so
a category drawn in grey reads as chrome. `Queued` was `#8A929E` dark and `#5F6B7A` light: both
about **9% saturation**, a grey with an opinion.

**Queued becomes yellow; Retrying becomes a bolder, darker orange** so the two stay apart, being
neighbours in one legend where yellow/amber is the pair at risk.

**The rule:** colour may be reclaimed between pages. An overlap is only a problem when two
different kinds of thing claim the same colour *on the same page*.

The palette comment asserts something stronger and false — that discovery colours avoid kind hues
so as not to "mean two things at once". In dark mode today `retrying` and `composite_action` are
both `#FFA94D`, `running` and `repo` are both `#4DABF7`, `unavailable` and `action` are both
`#FF6B6B`. Three of five already collide. Replace the comment with the page-scoped rule.

- **Discovery** renders the discovery palette alone — no kind colours on it. Yellow is free.
- **Explorer, frontier view** renders the discovery legend *and* the kind rows below it as node
  filters. That page is already oversubscribed by three; yellow meets `org` (`#FFD43B`) and makes
  four. The defect there is drawing kind swatches in their hues while hue means state, which
  deserves its own entry rather than blocking the queue colour.

**Light is the hard case.** Dark is easy: `#FFD43B` clears the 3:1 floor against `#1A1B1E`
comfortably. A yellow bright enough to read as yellow cannot clear 3:1 against white — `#FFD43B`
manages about 1.4:1 — so the light-mode queue colour is necessarily dark and reads closer to amber
or olive, with `retrying` beside it. Size it before picking the values.

**Changing `WarningDark` moves every warning in the interface**, because MudBlazor reads the
semantic colours from the theme. That is the consistent choice, but the contrast tests have to
cover the components too, not only the swatch.

**Close the class, not the instance.** The guard from the last grey asserts queued is not
byte-identical to `Muted` and sits 1.5:1 from it. `#8A929E` passes both and is still grey: the
assertions describe the bug that was found, not the property wanted. Add a **chroma floor** for
every colour carrying a category and a **hue-distance floor** within each legend.

## What it measured

On the reference corpus, with 33 verified accounts:

| | Before | After |
| --- | --- | --- |
| Tier split | 448 / 689 | **16 / 439 / 682** |
| Suites offered | 3 | **2** |
| `queued` saturation, dark | 9% | **94%** |

The three tiers sum to 1,137, which is the done count exactly, so nothing is counted twice. The
16 owned are 13 from `github`, 2 from `GitHubSecurityLab` and 1 from `advanced-security`.
Migrating a copy of the real database seeded `owned` with both of `direct`'s suites and left
`direct` and `transitive` untouched, so the first round after the upgrade runs what it ran before.

**The hue floor could not be set where it was first written.** 25° is unsatisfiable: three of the
five states are warm — unavailable red, retrying orange, queued yellow — and the warm arc is about
60° wide, so a 25° floor leaves no slack and forces a semantic colour off its meaning. It is 20°,
with the trio at roughly 0°, 29° and 53°.

**Light mode confirmed the prediction rather than escaping it.** A yellow bright enough to read as
yellow cannot clear 3:1 against white, so light-mode `queued` is `#9A7B00` — the same 48° hue as
its dark counterpart, and visibly olive. That is the contrast floor's price, not a second choice
of hue.

**One existing test asserted the behaviour that changed.**
`TestSuitesFromEveryPackAreOffered` counted three suites from three packs. It is now
`TestSuitesFromEveryAnalysisPackAreOffered` and counts two, with the CI-only pack covered
separately — renamed rather than loosened, so the count still means something.

## Pending

1. **Cost guidance that is measured, not guessed.** The control states *how many* dependencies a
   change applies to; it does not state how long. "This tier, this suite, ~N repositories at ~M
   seconds each" needs the cycle timings that are already logged and nothing surfaces.
2. **Re-analyze a selection**, not only a whole tier: the Discovery table already supports
   filtering, so acting on the current filter is the natural extension.
3. **Staleness per suite.** A row now says *when* it was last analyzed, but not *with what*. Once
   the run's pack is joined to the row, a tier can show which of its suites are behind rather than
   an undifferentiated "re-analyze everything". See `db/analysis-identity.md`.
4. **The grid costs 304px and never folds**, which is now the largest thing between a reader and
   the queue. That is a page-layout problem rather than a control problem, so it lives in
   `ui/discovery-config.md`.

## Constraints worth restating

- Naming: suites are named for the query set they contain, never for the tier they default to.
  A tier binds to a *set* of suites, so no combined per-tier suite is ever needed.
- Requeueing must keep what is already known. Clearing it would blank the Explorer for as long
  as the re-run takes, which is the opposite of what re-processing is for.
- Requeueing charges no attempt: nothing failed.

## Verification

- bUnit: the control states its count; an unknown tier is refused.
- Real corpus: requeue a tier, confirm the queue fills and the counts move as predicted.
