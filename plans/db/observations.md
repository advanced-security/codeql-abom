# The observation store

**Status:** Built and carrying real data. The table is keyed on the statement, so re-running a
query refreshes its answers rather than accumulating copies of them.

## Why this exists

Query output that is neither a dependency edge nor a security finding had nowhere to go, so a new
query meant a schema change and a new ingest path. `abom_observation` is the generic destination: a
query nobody has written yet contributes facts without a column, a migration, or a line of Go.

What it may never do is contribute an **unattributed** fact, which is why `query_run_id` is `NOT
NULL`. That is the same rule that makes a finding name its rule id, and the whole model rests on it.

## Pending

1. **The subject-key gap.** Some subject keys do not join to an `abom_node` row — two of 59 in the
   first sample, against **9,093** distinct subject keys now recorded. Probably jobs in files not
   otherwise represented, but worth confirming: the alternative is a key format disagreement that
   silently orphans facts.
2. **Surfacing.** `/api/node/{key}/-/observations` exists and nothing renders it. The reason not to
   has gone: the busiest subject on the corpus answers 8 rows, one per attribute plus a single
   genuine disagreement. This is a UI task with no store defect behind it.

## The constraints that must not be broken

**Two queries may disagree about one subject and both are kept.** Collapsing them would silently
pick a winner, which is a judgement this layer does not make. That is why the unique key ends in
`value`: deduplication is keyed on the statement, not on the fact. Live on the corpus, `runsOn` is
recorded twice for one job — `macOS-latest,…` and `macos-latest,…` — because the repository
changed the casing and nothing here is entitled to decide which is right.

**`HasNumber` distinguishes an absent measurement from a measured zero**, which is why `value_num`
is separate from `value`. `value` is in turn `NOT NULL DEFAULT ''`, because NULL never equals NULL
in a UNIQUE constraint, so a nullable value could not deduplicate against itself.

## Open questions

- **Append-only, or not.** `abom_node`, `abom_edge` and `abom_finding` are append-only and guarded
  by `TestGraphTablesAreAppendOnly`. Observations deliberately update in place on re-run, and what
  updates is the *attribution* of an existing statement, never its value, because a different value
  is a different row. That is defensible, but it is a difference. The fork: state it in the schema
  comment as a deliberate exception, or split sightings off the way `abom_finding_sighting` does.
- **EAV, or a fixed shape.** Eight attribute names exist and seven appear the same number of times
  each, which is a fixed-width record stored as entity-attribute-value. `abom_surface` declares
  exactly those seven columns and **holds zero rows**; nothing writes it, and `subject_kind` still
  has one distinct value across every row. Three ways out: populate `abom_surface` and retire the
  EAV rows; record which runs re-confirmed a fact instead of copying it; or keep EAV as the price
  of a destination a query can write to without a migration. Left open deliberately — what remains
  is a modelling decision, and dropping `abom_surface` to tidy up would foreclose it.

## Verification

- `TestAQueryNobodyWroteCodeForCanStillContributeFacts` is the acceptance test for the whole idea.
  If it ever needs a code change to pass, the design has failed.
- Both read paths `SEARCH` rather than `SCAN`, asserted on the query plan rather than on the clock,
  because the cost is structural and a stopwatch on a small corpus passes either way.
- **An index added to `indexDDL` does not reach an existing corpus.** `CreateIndexes` applies it
  only after a bulk load, so anything the reads depend on is created by `Migrate`.
- A rebuild that deletes rows `VACUUM`s, or it returns a file larger than the one it shrank.
