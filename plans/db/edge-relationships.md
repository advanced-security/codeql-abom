# An edge says what boundary it crosses

**Status:** planned. Independent of the ownership work — this is a structural property of the
edges already stored, true regardless of who is running ABOM. `api/bill-of-materials.md` needs it.

## Why this exists

`abom_edge.repository_row_id` names the repository that **contains** the reference, not the one it
targets. Nothing anywhere says whether a dependency stays inside one repository, crosses to a
sibling under the same account, or leaves for someone else's account entirely.

Those are three different pieces of work and three different risks. A workflow calling a reusable
workflow in its own repository is a refactor away from being inlined. The same call across two
repositories of one organization is a coordination problem inside a team. The same call to another
account is a supply-chain dependency on a third party. Drawing them identically is the same
conflation that once inflated blast radius from 202 to 209 by counting upstream repositories as
dependents.

A bill of materials has to state it, because "what is this composed of" is answered differently
depending on where the pieces come from.

## What is already true

| | |
| --- | --- |
| edge kinds | `step-uses-action`, `composite-step-uses-action`, `job-needs-job` |
| `job-needs-job` | ordering, not dependency — excluded by `store.DependencyEdgeFilter` |
| boundary classification | **none** |
| direct vs indirect | computed by recursive walks in `BumpImpactOf` and `BuildExposure`, never stored |

Both endpoints already carry what is needed: `abom_node` has `owner`, `repo` and
`repository_row_id`, and the edge has the containing repository. The classification is derivable
today and is derived nowhere.

## The design

A stored class on the edge, computed from its endpoints at ingest:

- **same repository** — source and target belong to one repository.
- **same owner** — different repositories, one account.
- **cross owner** — different accounts.

Deliberately *not* a fourth value for "owned versus not". That is a property of the operator's
declaration, changes when a declaration changes, and belongs to the bill of materials rather than
to the edge. The three above are properties of the world.

**Direct versus indirect stays out of this plan.** It is relative to a root — an action is direct
to the workflow that names it and indirect to the repository two hops up — so it is a property of
a walk, not of an edge. `api/bill-of-materials.md` computes it against whatever root the document
is for.

### Backfill

The classification is derivable from rows already stored, so existing edges are filled by a
one-pass migration rather than by re-analysis. That is the whole reason to store it rather than
join for it: the join is three table lookups per edge on a read path the Explorer hits constantly,
and `EXPLAIN QUERY PLAN` is the check before committing to either.

## Phases

1. The classification as a pure function of two node rows, tested against the fixture, which
   already contains all three cases — `octo-org/tool` naming `actions/checkout` is cross-owner,
   `octo-org/app` naming `octo-org/shared`'s workflow is same-owner.
2. The column, written at ingest.
3. The backfill migration for existing edges.
4. Surfaced: an Explorer edge filter, and the field the bill of materials reads.

## Verification

- Counts by class on the real corpus, cross-checked against the same classification computed
  independently in SQL. Exact agreement.
- A node with no repository — an external action nothing has analyzed — classifies as cross-owner
  rather than as unknown collapsing into same-repository. Absent must not read as a value.
- `job-needs-job` edges are classified but stay excluded from dependency traversals;
  `DependencyEdgeFilter` is untouched.
- Backfill is idempotent: running it twice changes nothing.
- The read path the Explorer uses does not get slower — measured, since a new column on a hot
  table is the sort of thing that quietly costs a page.

## Open questions

- **Enterprise and host boundaries.** `github.com` versus a GitHub Enterprise Server host is a
  fourth kind of crossing, and `abom_account` already records a `host`. Not modelled here because
  the corpus has one host; the classification should be shaped so a fourth value can be added
  without rewriting the first three.
- **Forks.** A fork of an upstream action is cross-owner by name and something else in spirit.
  ABOM has no fork relationship today and this plan does not invent one.
