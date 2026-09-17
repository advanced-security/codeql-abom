# Failure signatures

Data-shape failures this repository has already paid for. Each entry is: the symptom as it
presents, how to detect it from a measurement rather than a hunch, what it cost, and what fixed it.

Use this in phase 4, after measuring. Reading it first biases the measurement toward the failures
already known, and the point of the census is to find the ones that are not here yet.

**A measurement matching nothing here is a contribution.** Add it, with its numbers.

---

## SQL and storage

### 1. Correlated subquery evaluated per row

**Symptom** — an endpoint whose cost scales with result count rather than with the work it
describes. Feels like "the database is slow"; it is one expression in the select list.

**Detect** — `EXPLAIN QUERY PLAN` reports `CORRELATED SCALAR SUBQUERY`. Confirm by timing the
query with the expression removed: if cost collapses, that is it.

**Cost paid** — 17.7s against 0.045s once fixed. **393x**, one expression.

**Fix** — hoist the per-row lookup into a `GROUP BY` rollup CTE and join it once. The aggregate
runs once over the whole table instead of once per outer row.

**Watch** — the rollup's `SUM`/`MIN`/`MAX` return NULL over an empty set where the correlated form
returned nothing at all. `COALESCE`, and check that absent has not become zero.

---

### 2. Recursive CTE forming a cross product

**Symptom** — a traversal that returns the right answer in minutes. Multiplicative, not additive:
doubling either side quadruples it.

**Detect** — compare rows visited against rows returned. In the case paid for, 4,970 × 201 was
being enumerated to produce a few thousand rows.

**Cost paid** — 36s.

**Fix** — close over the *targets* first, in one recursive pass, then join the call sites to the
closed set. The recursion should walk the smaller side.

**Watch** — a recursive CTE with two unconstrained joined relations in its recursive term is this
shape whether or not it is slow yet.

---

### 3. Join predicate no index can serve

**Symptom** — an index exists, is correct, and is not used.

**Detect** — the plan shows `SCAN` on a table you indexed. Look at the join predicate: a function
call, a cast, a `COALESCE` or a concatenation around a column defeats the index on it.

**Cost paid** — `AdvisoriesByReach` joins `n.owner || '/' || n.repo = p.package_name`. Every
advisory row scans every node.

**Fix** — store the joined form as a column, or index the expression, or restructure so the
comparison is column to column. Which one depends on write cost, which the proposal must state.

---

### 4. Bipartite selectivity collapse

**Symptom** — a filter that keeps a reasonable number of nodes produces a picture with no
structure in it. Looks like a rendering bug and is a data-shape fact.

**Detect** — never measure a graph filter by nodes kept alone. Measure **edges surviving between
kept nodes**, and the share of the match that is isolated within it.

**Cost paid** — measured 2026-09-10 on 4,254 nodes over 11,307 edges:

| predicate | nodes kept | edges kept | isolated within the match |
| --- | ---: | ---: | ---: |
| `minReach>=5` | 149 | 141 | 0% |
| `origins=seed` | 594 | 669 | 0% |
| `hasFindings` | 536 | 148 | 74% |
| `kinds=action` | 1,420 | 109 | 89% |
| `hasAdvisory` | 232 | **1** | 99% |

The graph is bipartite by kind — a workflow's edges run to actions, never to workflows. **Reach and
origin cut across the structure; kind and findings cut along it.**

**Fix** — not a fix, a design rule. A predicate that cuts along the structure must not become a
server-side node filter for a view whose job is to show relationships. It belongs to an endpoint
that answers a *set*, where structure is irrelevant — which is exactly what a policy gate wants.

**Watch** — this generalizes. Before pushing any filter into SQL for a graph consumer, measure its
edge survival. The node count will look encouraging in every case.

---

### 5. Unindexed column that everything filters on

**Symptom** — nothing dramatic. A read path that is slower than its row count justifies.

**Detect** — cross-reference the predicated-column census from phase 1 against the 24 indexes. Any
column with high cardinality, frequent filtering and no index is a candidate.

**Cost paid** — one missing index cost 198 ms per page against 9.7 ms. `abom_frontier.ref` is
currently filtered and unindexed.

**Fix** — index it. **State the write cost**: `CreateIndexes()` runs after bulk load, so an index
needed by a page on a table `serve` reads belongs in `Migrate()` instead — and
`TestIndexesAreNotCreatedByMigrateOnTheTablesItLoadsInBulk` guards which tables that applies to.

---

### 6. The planner has no statistics

**Symptom** — plans that are defensible in the abstract and wrong for this corpus. Join order that
ignores which side is small.

**Detect** — `SELECT count(*) FROM sqlite_master WHERE name='sqlite_stat1'` returns 0. It does on
any database compaction has never run against, which is most of them.

**Cost paid** — not yet quantified. `ANALYZE` runs only inside compaction, beside `VACUUM`, so any
database that has not been compacted has never been analyzed.

**Fix** — candidate, not a conclusion. Measure the same plan set before and after `ANALYZE` on a
copy. If plans change, the proposal is about *when* `ANALYZE` runs, not whether.

**Watch** — it is also a measurement variable. Never compare a timing from before it to one after.

---

## Traversal and in-memory structure

### 7. N+1 inside a loop

**Symptom** — cost proportional to result count, in Go rather than in SQL.

**Detect** — count *calls*, not elapsed time. A test that asserts the number of store calls catches
this and keeps catching it; a test that asserts a duration is flaky and proves nothing.

**Cost paid** — `referencesTo(nodeKey)` called once per advisory node.

**Fix** — one query returning all the rows, grouped in memory by key.

---

### 8. Repeated map construction over the same set

**Symptom** — memory and time both higher than the algorithm suggests, with no single hot spot.

**Detect** — count map builds and allocations across a request, not just wall time. Seven maps over
one node set is structural even when it is fast.

**Cost paid** — in `internal/graph`, seven in-memory maps built over the same nodes.

**Fix** — build the adjacency once and pass it down, or make one pass carry what the others each
re-derive.

---

### 9. A process started per call

**Symptom** — a tiny unit of work that takes the better part of a second.

**Detect** — time the work itself separately from the invocation. If they differ by two orders of
magnitude, the cost is startup.

**Cost paid** — 0.75s against 0.004s (**188x**) and 0.62s against 0.004s (**155x**).

**Fix** — a resident server, invoked repeatedly.

**Watch** — this also invalidates benchmarks. A per-invocation timing harness attributes 99% of the
cost to the wrong layer, so time against the resident path.

---

## Payload and client

### 10. Edges that restate a node field

**Symptom** — payload larger than the graph justifies; the browser slow in proportion to it.

**Detect** — group the edges by kind and ask, for each, whether the fact is already on a node.

**Cost paid** — 9,257 of 30,853 edges (**30%**) were containment edges, each restating the
`container` field the node already carried.

**Fix** — derive them client-side from the node field.

**Watch** — derived is not free. State what the client now has to build, and measure that too.

---

### 11. Repeated keys in a JSON array

**Symptom** — bytes per element far above the information per element.

**Detect** — divide payload bytes by element count and compare to the fields actually carried. 818
bytes per node against 13 repeated keys.

**Cost paid** — 8.0 MB payload; **473 KB gzipped**.

**Fix** — a columnar or tabular encoding, if the measurement supports it.

**Watch** — **quote both numbers.** Gzip removes most of the repeated-key cost on the wire, so the
saving is in parse and allocation, not transfer. A proposal quoting only the uncompressed figure
predicts a network win that will not appear.

---

### 12. A client filter that hides without releasing

**Symptom** — filtering to a small subset does not make the browser faster, and memory grows.

**Detect** — measure heap, not frame rate alone. 419 MB to 643 MB while filtering *down*.

**Cost paid** — 3.4 FPS at 9,811 nodes against 60 FPS at 430.

**Fix** — remove the objects from the simulation rather than marking them invisible.

---

## Measurement failures

These produce wrong conclusions rather than slow code, and are the most expensive entries here
because they are invisible.

### 13. A cursor invalidated by a missing field

**Symptom** — an incremental path that is correct and never incremental.

**Detect** — check the distribution of the field the cursor compares. `MAX(epoch)=1` on generations
185 and 186 only, and 0 on everything else, means the field is being defaulted rather than written.

**Cost paid** — `RecordGraphGeneration` omits `epoch`, so every delta answers `reset` and the
client reloads the full payload on every poll. The delta mechanism exists and never runs.

**Fix** — write the field. **Then re-measure the thing the delta was supposed to make fast**, since
it has never actually been exercised.

---

### 14. A cursor taken after the rows it should see

**Symptom** — a test that passes, or skips, and proves nothing. A delta test returning empty.

**Detect** — a test asserting on a non-empty result that does not *also* assert it is non-empty.

**Cost paid** — a delta scope leak sat behind a vacuously green test.

**Fix** — take the cursor before seeding. And assert the precondition, so the test fails rather
than skipping when it stops holding.

---

### 15. A metric insensitive to the work it measures

**Symptom** — a real improvement moves the number not at all.

**Detect** — before trusting a metric, predict what it should do and check that it does.

**Cost paid** — "functions over complexity 10" stayed at 50 while a function went from 22 to 11. It
counts functions crossing a threshold, so halving one that remains above it changes nothing.

**Fix** — measure **total excess over the threshold** (204 on this codebase), which moves whenever
any function improves.

**Watch** — thresholded counts, averages over skewed distributions, and ratios with an unstated
denominator all fail this way.

---

### 16. A corpus that cannot demonstrate the thing being tested

**Symptom** — a filter returns a byte-identical payload and looks broken.

**Detect** — before concluding a mechanism is inert, check whether the data contains anything it
would exclude. `scope=owned` narrowed nothing because the closure is 499 repositories, 461 carry
nodes, and **all of them are inside it**. Nothing to drop.

**Cost paid** — nearly a wasted investigation into working code.

**Fix** — none needed. Record that the corpus cannot demonstrate it, and prove the mechanism on a
fixture that can.

**Watch** — the inverse is the real risk: a corpus that cannot demonstrate a failure will not
demonstrate a regression either.

---

### 17. The safe-looking command is the dangerous one

**Symptom** — `sqlite3 "file:$DB?mode=ro"` fails with `unable to open database file (14)` against a
snapshot that `PRAGMA integrity_check` says is fine, and succeeds against the live database.

**Detect** — the asymmetry itself. A read-only form that works on one file and not on its own
`.backup` is not about the data.

**Cost paid** — caught while testing this skill, 2026-09-10. A WAL database needs a `-shm` file, a
read-only connection may not create one, and `.backup` does not write one; the live database only
works because the server already made its `-shm`. **The natural reaction is to go back to the file
that worked — which is the live database.**

**Fix** — `immutable=1` on every read. It works on both, and unlike `mode=ro` it actually
guarantees the file cannot be written.

**Watch** — generalizes past SQLite. Any safety measure that fails only on the safe target trains
people out of using it.

---

### 18. A timing table that does not check status codes

**Symptom** — an endpoint looks impossibly fast and returns a suspiciously small payload.

**Detect** — record the **status code** beside every timing. `curl -w '%{http_code}'`.

**Cost paid** — first sweep, 2026-09-11. `/api/bom` at 88 bytes in 1 ms and `/api/prune` at 92
bytes in 33 ms were recorded as fast paths. Both were **HTTP 500**, and the bytes were the error
message. They went into the review as measurements and had to be retracted.

**Fix** — status code in the table, every time. An error path is the fastest path in any system.

---

### 19. A disabled feature measures as a free one

**Symptom** — an expensive-looking fragment of a query plan costs nothing, and the explanation
offered is that the tables are small.

**Detect** — before concluding a code path is cheap, **check that the data makes it run at all**.
Count the rows in every table its predicate touches.

**Cost paid** — first sweep, 2026-09-11. `ownedFullName` wraps a join column in
`SUBSTR`/`INSTR`/`LOWER` and nests two `EXISTS`, appearing in the plan as four nested scans, twice.
It measured **0.00 s** — because `abom_owned_scope` holds **zero rows** on the reference corpus, so
`abom_owned_scope_honored` is an empty view and every `EXISTS` over it is false immediately. The
fragment was about to be written up as a proven non-issue.

**Fix** — report it as unmeasured rather than as cheap, and say which data state would exercise it.

**Watch** — this is signature 16 wearing the opposite face. There, a corpus could not demonstrate a
feature *working*; here, it could not demonstrate one *costing*. Both come from assuming the corpus
exercises the code.
