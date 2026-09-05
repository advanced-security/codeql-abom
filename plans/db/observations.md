# The observation store

**Status:** built and carrying real data; hardening pending.

## Why this exists

Query output that is neither a dependency edge nor a security finding had nowhere to go, so a new
query meant a schema change and a new ingest path. `abom_observation` is the generic destination:
a query nobody has written yet contributes facts without a column, a migration, or a line of Go.

What it may never do is contribute an **unattributed** fact, which is why `query_run_id` is
`NOT NULL`. That is the same rule that makes a finding name its rule id, and the whole model
rests on it.

## Shape, and why

```sql
abom_observation(row_id, query_run_id NOT NULL, subject_kind, subject_key,
                 name, value, value_num, file_path, start_line,
                 UNIQUE(query_run_id, subject_kind, subject_key, name))
```

- **Subjects named by key, not row id.** A query emits the node key it saw, and the observation
  must survive being recorded before the subject it names exists.
- **`value_num` separate from `value`**, so thresholds and ordering do not depend on string
  collation. `HasNumber` distinguishes an absent measurement from a measured zero — the same
  distinction the reach watermark exists to protect.
- **The UNIQUE key means re-running refreshes rather than accumulates.** A database analyzed
  twice must not report every fact twice, or any count drawn from it drifts upward on its own.
- **Two queries may disagree about one subject and both are kept.** Collapsing them would
  silently pick a winner, which is a judgement this layer does not make.

## Contract

Required: `subjectKey` (or `subjectPath`), `name`. Optional: `subjectKind`, `value`, `valueNum`,
`filePath`, `startLine`, `subjectPath`/`subjectJob`/`subjectStep`.

A query is evaluated against one repository's database and **cannot know which repository that
is**, so it may name a subject by where it sits and the ingester builds the node key with
`normalize.JobKey`/`WorkflowKey`/`StepKey`. Steps are addressed by position because `id:` is
optional and usually absent.

Live: `abom/surface` produced 1,057 facts; `abom/step-conditions` 67. 57 of 59 subject keys from
one run joined to existing `abom_node` rows.

## Pending

1. **Indexes for the read patterns that emerge.** There is one on `(subject_kind, subject_key)`.
   Querying by `name` across subjects — "every privileged job" — has none yet, and the shape of
   those queries is not yet known well enough to guess.
2. **The two-of-59 gap.** Some subject keys did not join to a node. Probably jobs in files not
   otherwise represented; worth confirming rather than assuming, because the alternative is a key
   format disagreement that would silently orphan facts.
3. **Retention.** The table is not truncated by the aggregate rebuild, and re-runs refresh rather
   than accumulate, so it grows with distinct (query, subject, name). No pruning story yet.
4. **Surfacing.** `/api/node/{key}/-/observations` exists; nothing renders it.
5. **Append-only question.** `abom_node`/`abom_edge`/`abom_finding` are append-only and guarded
   by `TestGraphTablesAreAppendOnly`. Observations deliberately update in place on re-run. That
   is a defensible difference — but it is a difference, and it should be stated in the schema
   comment rather than inferred.

## Verification

`TestAQueryNobodyWroteCodeForCanStillContributeFacts` is the acceptance test for the whole idea.
If it ever needs a code change to pass, the design has failed.
