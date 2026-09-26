---
name: regenerate-abom-fixtures
description: 'Regenerate or diagnose codeql-abom test fixtures — BQRS, SARIF and testdata JSON. Use when a fixture is stale, when a test needs different sample data, when a test fails on data shape, when adding a testdata file, or when tempted to hand-write or hand-edit BQRS/SARIF JSON. Covers why fixtures must come from the CodeQL CLI, the fixture source trees under ql/actions/graph-extraction/test, the three packages holding identical copies, and how hand-built graph fixtures drift from real ingestion.'
---

# Regenerate ABOM test fixtures

## Never hand-write or hand-edit CodeQL output

BQRS and SARIF fixtures come from the CodeQL CLI and are checked into `testdata/`. Constructing
them in Go looks right, compiles, and lies.

A fake that emitted `startLine = 1` for every tuple collapsed every edge into one row, because
`normalize.EdgeKey` includes source location and `abom_edge` upserts on `edge_key`. The symptom
was a discovered dependency missing from `abom_frontier`, and several rounds went into hunting a
phantom bug in `frontier.Enqueue`, which was correct all along.

**If a test needs different data, change the fixture source tree and regenerate.**

Corollary worth checking on any fixture: if its tuples share a location or other identity field,
it is not exercising the identity logic at all.

## Regenerate

```bash
.github/skills/regenerate-abom-fixtures/scripts/regen-edges-fixture.sh
```

The script refuses to run while discovery is going, because every `codeql` invocation shares one
compilation cache. Pause with `make stop` first.

| | |
| --- | --- |
| Fixture source tree | `ql/actions/graph-extraction/test/AbomEdges` |
| Query | `ql/actions/graph-extraction/src/AbomEdges.ql` |
| Written to | `internal/bqrs`, `internal/expand`, `internal/ingest` (`testdata/abom-edges.json`) |

Those three copies are byte-identical and must stay so. Regenerating one by hand drifts the
others, and the tests that read them start disagreeing for no visible reason.

Read the diff before accepting it. A fixture change is a change to what every test using it
means.

### The corpus query pack

`testdata/corpus/authoring.json` is what CodeQL said over the operator's pack `make corpus` lays
out: each test run, the report it left, and one `PrintAST` tree. Record it with the CLI, never by
hand:

```bash
go run ./cmd/abom-corpus --record-queries testdata/corpus/authoring.json
```

`scripts/regenerate-corpus-fixtures.sh` runs this last. It installs both scaffolded packs first,
because an unlocked pack does not compile, and strips the scratch path from the tree. The queries,
fixtures and steps live in `corpusQueries` in `cmd/abom-corpus`. Change them there and record
again: a build refuses a recording made over other files, naming this script.

`internal/runner/testdata/testrun-*.json` are `codeql test run --format=json` output, captured over
a scaffolded pack in a scratch directory. The `.actual` beside a RESULT failure is part of the
fixture, because the expectation counts are read from it.

## Real fixtures earn tests you would not have written

The real `abom-edges.json` contains `docker://alpine:3.20` and `actions/checkout` at two
different refs, which is where `TestExpandDoesNotQueueUncloneableTargets` and
`TestExpandQueuesAnActionOnceAcrossRefs` came from. Read a regenerated fixture for cases you did
not plan.

## When a test fails on data shape, suspect the fixture first

Hand-built *graph* fixtures — Go structs, not CLI output — drift from what ingestion actually
produces. Three separate false alarms:

| Fixture omitted | Real ingestion sets | Broke |
| --- | --- | --- |
| `Owner`/`Repo` on external nodes | both, always | advisory matching (`owner \|\| '/' \|\| repo`) |
| workflow nodes entirely | one per workflow file | workflow-level finding linkage |
| `Path` on workflow nodes | `.github/workflows/ci.yml` | finding-to-node fallback |

Check reality before changing code:

```bash
sqlite3 .abom/db/abom.db \
  "SELECT node_key, node_kind, path, owner, repo FROM abom_node LIMIT 5;"
```

Then make the fixture match, and expect node-count assertions to move.

A fixture with no queue rows, no findings, or no upstream repositories is not proof the code
handles those; it is proof the case was never reached. Confirm against the corpus.

## Query test expectations

The test trees double as fixture sources, so changing one means regenerating the Go fixtures too.

```bash
codeql pack install ql/actions/graph-extraction/test   # from the workspace root
codeql test run ql/actions/graph-extraction/test
```

A test with no `.expected` reports as failing. Read the `.actual` and review it **before**
`codeql test accept` — accepting blind bakes in whatever the query currently does, bugs included.
