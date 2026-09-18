# What the extended analysis reveals, and whether any existing number should move

**Status:** Undecided by design. The closure database is built to change nothing; this is where the
decision to let it change something gets made, once there is something to look at.

## Why this exists

[`ql/closure-database.md`](../ql/closure-database.md) deliberately produces a database that moves no
existing total: every row it writes is `extended`, and every aggregate reads `regular`. That is the
right default — a number that changes for an undeclared reason is the failure mode
[`plans/next.md`](../next.md) keeps recording — but it is a default, not an answer. Three kinds of
fact become available for the first time and none of them has a home yet.

**A resolved callee.** A `uses:` line whose target is now a known callable rather than a string.
Today `abom_edge` records the reference as written and nothing about what it resolved to inside
CodeQL. The spike's seven-step path exists because the library resolved three of them.

**A route that crosses a `uses:` boundary.** Taint from an externally-controlled value in an owned
repository's workflow into a `run:` inside a dependency, three hops away. Nothing in ABOM can
currently express that a *path* exists, only that an edge does.

**A finding on a dependency, seen from a consumer.** The same upstream defect is already reported
when that dependency is analyzed in its own right. Seen through a consumer's closure it is the same
defect with a different question attached: not *is this broken* but *does it reach you*.

The last one is where the double-counting lives, and why this plan is separate from the one that
builds the database. Reach is computed from `abom_edge`. Dropping extended edges into that table
without a decision means reach changes, the crawl reorders, every node the Explorer draws resizes,
and half the remediation arithmetic moves — **all without anybody choosing it**, because the
aggregate does not know its inputs changed meaning.

## Pending

1. **Look at what is actually there, before deciding anything.** Against one owned repository's
   closure: how many `uses:` lines resolved that the per-repository analysis left unresolved, how
   many cross-boundary routes exist, and how many findings are newly attributed that the regular
   analysis already reported elsewhere. **This plan cannot be written further until that number
   exists**, and the legitimate outcome is that the extended rows are interesting to read and change
   no aggregate. Depends on the closure database.
2. **Decide whether a resolved callee is a new edge or a new fact about an existing one.** The
   `uses:` line is already an edge; the resolution is a statement *about* it, which is the shape
   `abom_observation` exists for — it is keyed by the statement rather than the run and already
   refuses to pick a winner between two readings. A second edge for the same `uses:` line would
   double-count it in every traversal. **The alternatives are named rather than assumed**: a column
   on `abom_edge`, an observation row, or a separate relation. Depends on 1.
3. **Decide what a cross-boundary route is stored as, if anything.** A CodeQL path is a sequence of
   locations, and ABOM has no table with a sequence in it. The cheap reading is that a route is
   already implied by the edges it traverses and needs no storage; the expensive reading is that the
   route is the product and the edges are the incidental part. Depends on 1.
4. **Decide whether extended rows enter reach.** The decision this plan is named for. It is
   deliberately last, because 1 to 3 are what make it answerable, and because the answer may
   legitimately be no. If it is yes, it arrives as a **stated before-and-after diff against the
   crawled organization**, never as a migration that lands quietly. Depends on 2 and 3.

## The constraints that must not be broken

**A deferral only stays open while its default changes nothing.** This is the whole reason the plan
exists as a separate file. Extended rows are written, are filterable, and are read by no aggregate
until item 4 decides otherwise. If anything starts reading them before that, the decision has been
made by omission, which is the outcome this plan is designed to prevent.

**The same defect in the same dependency is one finding.** `abom_finding` is unique on
`(rule_id, repository_row_id, file_path, start_line, start_column, fingerprint)` and `result_hash`
deliberately excludes the repository so one defect across many repositories groups by content. A
dependency appearing inside twenty closures must not become twenty findings — it is one finding
reached twenty ways, and the twenty ways are edges.

**The dependency owns the finding; the consumer has exposure.** Unchanged, and restated here
because a closure database is exactly the thing that would tempt a reattribution. A defect in
`actions/checkout` is `actions/checkout`'s, however it was reached.

**A security verdict still comes only from a rule id.** *Reached across a boundary* is a
measurement. Whether it matters is a rule's call, and this plan adds no verdict of its own.

**Absence on the extended side is not zero.** A repository with no closure database has no extended
answer, which is not the answer none. The same rule coverage already follows.

## Verification

- Item 1's numbers are recorded before item 2 starts, and recorded again if the closure changes
  shape.
- Every aggregate still names its mode, and the test that enumerates them from source still passes.
- If item 4 concludes yes: reach, blast radius and the remediation ordering are diffed against the
  crawled organization, the diff is read, and the entries that moved are explained individually
  rather than in aggregate.
- If item 4 concludes no: that is written down with its reason, and the extended rows keep being
  readable through the `mode=` term and nothing else.
- A dependency inside many closures produces one finding row and many routes to it, asserted
  structurally rather than by count.
