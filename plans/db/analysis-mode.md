# An analysis says how its database was built

**Status:** Nothing records the shape of the database a result came from, and a second shape is
about to arrive that answers a different question with the same rule ids.

## Why this exists

Every CodeQL database ABOM has ever built is one repository's own sparse checkout. That is the
shape GitHub's Default Setup builds too, so a rule firing in one of ours is reporting something a
developer could also be shown by their own code scanning.

[`ql/closure-database.md`](../ql/closure-database.md) introduces a second shape: the owned
repository plus its whole transitive closure, staged under `.github/actions/external/${owner}/${repo}/`
so that `uses:` resolves across repository boundaries. A rule that fires on a staged dependency
there is reporting **transitive exposure that GitHub's own scanning will never see**, because
Default Setup does not stage anything.

Same rule id, same repository, opposite meanings:

| Mode | How the database was built | What a result means |
| --- | --- | --- |
| `regular` | one repository's own sparse checkout | a defect in code that repository holds, of the kind Default Setup would also report |
| `extended` | an owned repository plus its staged closure | exposure reached through a `uses:` boundary, which nothing native reports |

Without the mode on the row, a reader cannot tell *"somebody will see this alert"* from *"nobody
will ever alert on this, and it still reaches you"* — and, worse, every existing aggregate silently
starts mixing the two the moment the second shape writes anything. Reach, the bill of materials,
the submission closure and the pinning counts are all computed over `abom_edge`, and none of them
would report that its inputs had changed meaning.

**This is a prerequisite rather than a companion.** It has to be in place before the first extended
row is written, because a total that changed for an undeclared reason is exactly the failure
[`plans/next.md`](../next.md) keeps recording: `make verify` stays green throughout.

Native resolution in [actions-lockfile.md](actions-lockfile.md) is a separate prerequisite for
staging the right revisions. `regular` versus `extended` describes database shape; lock context
describes which dependency commits a consumer selected. Neither field can substitute for the other,
and adopting native locks does not by itself add cross-repository dataflow to a regular database.

## Pending

1. **`analysis_mode` on `abom_query_run`**, with a fixed vocabulary of `regular` and `extended`,
   refused rather than defaulted when absent from a writer. It belongs on the run because it is a
   fact about the **database**, not about the finding — the same reason `cli_version` lives there.
   Depends on nothing.
2. **Carried onto `abom_finding` and `abom_edge`.** Both already reference `abom_query_run`, so the
   mode is reachable by join today. Whether it is denormalized onto the rows or joined per query is
   a cost decision this plan takes with a measurement rather than a preference:
   [`db/data-efficiency.md`](data-efficiency.md) already holds one endpoint that is linear in rows,
   and a join added to every read of the edge table is the sort of change that finds it. Depends
   on 1.
3. **A `mode=` term in the filter language.** Added to the enforceable declaration in
   `internal/view/spec.go` with its vocabulary, and applied on all three sides — the set path that
   `/api/match` and policy evaluation share, the graph path, and the per-unit listing terms in
   `internal/view/inventory.go`. A term declared on one side and silently dropped on another is
   what [`api/filter-enforcement.md`](../api/filter-enforcement.md) exists to stop, so this term
   arrives enforced on every side that can enforce it or refused on the ones that cannot. Depends
   on 1.
4. **Every aggregate states which mode it is over.** Reach, blast radius, the bill of materials,
   the submission closure and the pinning counts each filter explicitly rather than inheriting
   whatever is in the table. **An aggregate with no mode filter is the defect this plan exists to
   prevent**, so the absence is what the test looks for. Depends on 2.
5. **The interface differentiates without being asked to.** A reader looking at a finding sees
   which shape produced it, and can filter to one. Where a count mixes modes, it says so — the same
   rule the coverage strip already follows. Depends on 3.

## The decision this plan records rather than asks

**Existing rows are backfilled to `regular`, and that is a deliberate exception to the habit of not
backfilling.** `abom_finding.provider` was correctly left NULL on migration, because the tool that
produced an old row genuinely was not recorded. Here the opposite is true: only one writer has ever
existed, and it builds one repository's own checkout, so `regular` is a fact about the code that
produced every existing row rather than a guess about it. The exception is safe only while that
remains true, which is why a test asserts that no writer can record a mode before the column exists
— if a second database shape is ever added without this column, the backfill stops being a fact.

## The constraints that must not be broken

**A mode is a property of the database, not of the query.** The same query id runs in both shapes
and means different things in each. Recording the mode on the query, the suite or the pack would
make it un-askable for exactly the rule that most needs it.

**Mixing modes in one number is a defect, not a default.** A total over both shapes double-counts a
dependency that is both analyzed in its own right and staged inside somebody's closure. Where a
reader genuinely wants both, that is a stated union, not an unfiltered read.

**`extended` never silently becomes the source of an existing number.** Whether extended rows enter
reach at all is deferred to [`db/extended-findings.md`](extended-findings.md), and a deferral only
stays open if the default changes nothing. Until that plan decides, every aggregate reads
`regular` and the extended rows are inert.

**Absent is not a mode.** A row whose writer did not state one is refused at write time. This is
the store's absent-is-not-zero rule arriving in a column whose wrong value is indistinguishable
from a right one.

## Verification

- Every aggregate that reads `abom_edge` or `abom_finding` names a mode. A test enumerates them
  from source and fails naming any that does not — the same shape as the reachability guard that
  already fails naming unreachable packages.
- On the crawled organization, every total is **byte-identical** before and after the migration,
  because every row is `regular` and every aggregate reads `regular`.
- A fixture corpus holding both shapes returns different node sets for `mode=regular` and
  `mode=extended` on the set path, the graph path and each listing that declares the term.
- The term is refused where it cannot be enforced, rather than accepted and dropped.
- Mutation: flipping one row's mode changes exactly the counts that declare that mode, and no
  others.
