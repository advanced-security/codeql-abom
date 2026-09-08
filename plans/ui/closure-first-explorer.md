# The Explorer starts from what you own

**Status:** planned. Found by the interface audit on 2026-09-07. This is the entry that makes the
pruning work visible; without it the machinery exists and nothing reaches it.

## Why this exists

`db/pruning.md` shipped removal, a delta that reports it, and a compaction behind an epoch. On the
audited corpus it takes **56,830 nodes to 14,819** and **46,264 edges to 14,899**. None of it is
reachable from the interface: `prune` is a command-line subcommand, and nothing under `ui/`
mentions it.

So the Explorer's default view is still the whole corpus, and at that scale it is a hairball. The
audit captured it: **614 owner-groups gathered from 12,489 nodes**, collapsed, rendered as a
saturated pink starburst roughly 400 px across in which no single edge can be followed from one
end to the other. Only the outermost forty-odd spokes are traceable. This is the failure the
hairball test exists to catch, and the fix it prescribes is a smaller scope rather than a prettier
layout — which is exactly what the closure is.

The color makes it worse rather than better. With 614 owner-groups, almost every edge crosses
accounts, so the boundary classification paints the whole picture one color and carries no
information at this scale. **A classification is only legible inside a scope small enough for the
distinction to vary.**

There is a second, quieter defect. Boundaries are filled by a derivation that runs from
`graph.RebuildAggregates`, which only happens inside a discovery cycle. On a database migrated in
place and not yet crawled, **all 46,264 edges are unclassified** — audited directly. Every edge
then reports no boundary, and the Bill of Materials boundary filter returns nothing.

## What is already true

| | |
| --- | --- |
| prune | `codeql-abom prune`, `--apply`, `--compact`; no interface |
| `graph.OwnedClosure()` | repository-granular, membership only |
| `store.ExplainOutsideClosure` | reports what would be removed, without removing it |
| Explorer default | whole corpus, grouped by owner: 614 groups over 12,489 nodes |
| grouping by repository | **3,356 groups over the same 12,489 nodes** — worse, not better |
| Explorer scope filters | Origin, name, rule, reference, reach, advisory route |
| owned scope in the Explorer | **absent** — no filter, no marking, no mention |
| live updates | **already built**: `FollowGraph`, `GetGraphDeltaAsync`, `mergeInto`, `placeArrivals`, `FollowControl` |
| boundary on a freshly migrated corpus | **unclassified, all 46,264 edges** |

`ExplainOutsideClosure` is the important one: the interface can state what pruning would do
without doing it, which is what makes offering it safe. The live-update machinery is the other:
none of it needs building, and none of it is watchable at the scale it currently runs at.

## The design

### A scope the reader already has a word for

**The owned closure is the default, and an operator who has declared nothing sees nothing.** That
is decided. An empty Explorer pointing at Settings is the correct first-run experience, because
the alternative — showing the whole corpus until something is declared — is the conflation this
work exists to end, and it teaches a new operator that ABOM works without a declaration. It does
not. Nothing happens until ownership is claimed.

`FullGraphOptions.Scope` already exists and `ScopeExposure` already shows the shape. This is a
second value, computed from `OwnedRepositories()` and the same closure walk the crawler is bounded
by, so the picture and the crawl cannot disagree about what is in scope. The whole-corpus view
stays reachable and is named as the research view it is.

### Grouping stops being the default

`_groupBy` defaults to `"owner"` today, and switching scope auto-collapses. Grouping was a way to
make 12,489 nodes tractable and it does not work: at owner granularity the audit measured **614
groups** and at repository granularity **3,356 groups over the same 12,489 nodes** — more groups
than most corpora have repositories, and a worse picture, not a better one.

Inside the closure there is nothing to hide from. The default becomes **no grouping**, with owner
and repository grouping kept as tools for someone who deliberately widens the scope.

### The Explorer as the picture of discovery happening

This machinery is **already built and already wired**: `FollowGraph()` polls
`GetGraphVersionAsync`, fetches `GetGraphDeltaAsync`, and `mergeInto` and `placeArrivals` in
`graph.js` add arriving nodes beside the neighbors they arrived attached to, keeping the positions
of what is already placed. `FollowControl` sets the beat. None of it was written for nothing — it
is unwatchable, because an arriving node is one more speck in a saturated 12,489-node mass.

At closure scale it becomes the point: an operator declares an account, presses Start on Discovery,
and watches their supply chain draw itself. That is the most persuasive thing this product can do,
and it needs no new mechanism — only a scope small enough for an arrival to be visible, and the
grouping default out of the way.

### Pruning offered where its consequence is visible

`ExplainOutsideClosure` on the Discovery page, stated as a count of what falls outside, with the
removal behind an explicit action and the compaction behind a second one. The command already
separates explain, mark and compact for this reason; the interface must not collapse them into one
button.

**De-emphasized deliberately.** Pruning is not part of the normal loop — it is a thing an operator
does once after narrowing their scope, and it is destructive. It belongs in a quiet corner of
Discovery, below the controls anyone uses daily, stating plainly that **removal is not reversible
without re-analysis**. In the place where the button is, not in a tooltip.

### Classification that does not wait for a crawl

`ClassifyEdgeBoundaries` is idempotent and settles to zero. Running it when the server opens a
database costs nothing on a classified corpus and is the difference between a boundary filter that
works and one that silently returns nothing on every freshly migrated database.

## Phases

1. Classification on open, so the stored class exists before anything reads it.
2. The owned-closure scope in `FullGraph`, and the Explorer control for it.
3. The closure as the default, with an empty state that points at Settings when nothing is
   declared, and the whole-corpus view kept and named.
4. Grouping off by default.
5. `ExplainOutsideClosure` surfaced on Discovery, then prune and compact as explicit actions in a
   de-emphasized area.

## Verification

- The Explorer's default view on the audited corpus is the closure, and an edge can be followed
  from one end to the other — the hairball test, which the current default fails.
- With nothing declared, the Explorer is empty and says why, and does not render the corpus.
- Within the closure the three boundary classes are all present in useful proportion, rather than
  one class covering everything.
- **An arriving node is visible.** With discovery running against a declared scope, a node added
  between two polls can be seen to appear — the check the existing follow machinery has never been
  able to pass.
- A freshly migrated database serves classified edges before any cycle has run.
- `ExplainOutsideClosure` on screen matches the command's output for the same declaration.
- Pruning from the interface produces the same counts as the command, and every open page reloads
  on the epoch bump rather than showing rows that no longer exist.

## Open questions

- **What the empty state offers.** Pointing at Settings is the minimum. `ui/first-run.md` takes
  this further: enumerating a declared account's repositories is an API call rather than an
  analysis, so the Explorer can hold accounts and repositories within seconds of a declaration and
  fill in from there. The two plans share the "an arrival is visible" check.
- **Whether the whole-corpus view keeps grouping on.** It is the one scope where 12,489 nodes are
  still possible, and the audit says grouping does not rescue it either.
