# Measurement protocol

Exact commands, and the traps that make a number wrong without making it look wrong.

All of it runs against a **snapshot**, never the live database. Substitute `$DB` for the snapshot
path throughout; `$ABOM_DB` defaults to `/tmp/abom-real/abom.db`.

```bash
make status                                                          # discovery must be stopped
sqlite3 -readonly "file:$ABOM_DB?immutable=1" ".backup /tmp/abom-review.db"   # .backup, never cp
DB=/tmp/abom-review.db
sqlite3 -readonly "file:$DB?immutable=1" "PRAGMA integrity_check;"
```

**`immutable=1`, not `mode=ro`.** A fresh `.backup` of a WAL database has no `-shm` file, and a
read-only connection is not allowed to create one, so `mode=ro` fails with
`unable to open database file (14)` on a snapshot that is perfectly good. `mode=ro` appears to work
against the *live* database only because the running server has already created its `-shm` — which
means the form that looks like it works is the one you must not use, and the form that looks broken
is the correct one. `immutable=1` also guarantees no write can reach the file, which `mode=ro` on a
WAL database does not.

---

## Phase 1 — Census

### Snapshot identity

Record this first. Every later number is relative to it.

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
SELECT 'repositories', count(*) FROM repository
UNION ALL SELECT 'nodes',        count(*) FROM abom_node_live
UNION ALL SELECT 'edges',        count(*) FROM abom_edge_live
UNION ALL SELECT 'nodes (incl. tombstones)', count(*) FROM abom_node
UNION ALL SELECT 'edges (incl. tombstones)', count(*) FROM abom_edge
UNION ALL SELECT 'findings',     count(*) FROM abom_finding
UNION ALL SELECT 'frontier',     count(*) FROM abom_frontier
UNION ALL SELECT 'advisories',   count(*) FROM abom_advisory;"

sqlite3 -readonly "file:$DB?immutable=1" "SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size();"
```

Base and live differing means pruning has run. **From here on every query uses `abom_node_live` and
`abom_edge_live`** — benchmarking the base tables times work the application never does.

### Bytes per table and per index

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
SELECT name, SUM(pgsize) AS bytes, count(*) AS pages
FROM dbstat GROUP BY name ORDER BY bytes DESC;"
```

If `dbstat` is not compiled into the local `sqlite3`, fall back to `sqlite3_analyzer`, or report
row counts and note that byte attribution was unavailable. **Do not estimate it.**

Index bytes as a share of the total is the write-cost budget. This schema carries 24 indexes; any
proposal to add one is competing against whatever share they already hold.

### Degree distribution

Hubs decide whether a traversal is linear or quadratic, and a mean hides them completely.

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
WITH deg AS (
  SELECT src_node_row_id AS n, count(*) AS d FROM abom_edge_live GROUP BY 1
  UNION ALL
  SELECT dst_node_row_id, count(*) FROM abom_edge_live GROUP BY 1
), tot AS (SELECT n, SUM(d) AS d FROM deg GROUP BY n)
SELECT 'max', MAX(d) FROM tot
UNION ALL SELECT 'mean', ROUND(AVG(d), 2) FROM tot
UNION ALL SELECT 'nodes with degree > 100', count(*) FROM tot WHERE d > 100
UNION ALL SELECT 'nodes with degree > 1000', count(*) FROM tot WHERE d > 1000
UNION ALL SELECT 'isolated nodes', (SELECT count(*) FROM abom_node_live) - (SELECT count(*) FROM tot);"
```

Report the top ten hubs by name as well as the counts. A single action depended on by most of the
corpus is a different optimization problem from a uniform fan-out.

### Null rates on join keys

A join on a key that can be empty joins everything, and has.

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
SELECT 'node.repository_row_id',  SUM(repository_row_id IS NULL),     count(*) FROM abom_node_live
UNION ALL SELECT 'node.container_node_row_id', SUM(container_node_row_id IS NULL), count(*) FROM abom_node_live
UNION ALL SELECT 'edge.repository_row_id', SUM(repository_row_id IS NULL), count(*) FROM abom_edge_live
UNION ALL SELECT 'edge.ref_value',     SUM(COALESCE(ref_value,'') = ''), count(*) FROM abom_edge_live
UNION ALL SELECT 'frontier.resolved_sha', SUM(COALESCE(resolved_sha,'') = ''), count(*) FROM abom_frontier
UNION ALL SELECT 'query_run.db_commit', SUM(COALESCE(db_commit,'') = ''), count(*) FROM abom_query_run;"
```

### Cardinality and skew of predicated columns

An index on a low-cardinality, heavily skewed column will not be used and should not be proposed.

```bash
Q=(sqlite3 -readonly "file:$DB?immutable=1")
for col in node_kind is_external; do
  "${Q[@]}" "SELECT '$col', $col, count(*) FROM abom_node_live GROUP BY 2 ORDER BY 3 DESC;"
done
"${Q[@]}" "SELECT 'edge_kind', edge_kind, count(*) FROM abom_edge_live GROUP BY 2 ORDER BY 3 DESC;"
"${Q[@]}" "SELECT 'boundary', COALESCE(boundary,'(null)'), count(*) FROM abom_edge_live GROUP BY 2 ORDER BY 3 DESC;"
"${Q[@]}" "SELECT 'origin', origin, count(*) FROM repository GROUP BY 2 ORDER BY 3 DESC;"
"${Q[@]}" "SELECT 'severity', COALESCE(severity,'(null)'), count(*) FROM abom_finding GROUP BY 2 ORDER BY 3 DESC;"
"${Q[@]}" "SELECT 'provider', COALESCE(provider,'(null)'), count(*) FROM abom_finding GROUP BY 2 ORDER BY 3 DESC;"
"${Q[@]}" "SELECT 'rule_id (top 20)', rule_id, count(*) FROM abom_finding GROUP BY 2 ORDER BY 3 DESC LIMIT 20;"
```

### Filter selectivity — nodes *and* edges

The measurement most likely to change a design. **Node counts alone are misleading**; see failure
signature 4.

```bash
make start                                       # resident server; see the timing note below
curl -sS "localhost:8099/api/graph/full?steps=false" > /tmp/g-all.json
python3 - <<'PY'
import json, collections
g = json.load(open('/tmp/g-all.json'))
nodes, edges = g['nodes'], g['edges']
adj = collections.defaultdict(set)
for e in edges:
    adj[e['source']].add(e['target'])
    adj[e['target']].add(e['source'])

def report(name, pred):
    m = {n['key'] for n in nodes if pred(n)}
    if not m:
        print(f'{name:22} matched nothing')
        return
    iso = sum(1 for k in m if not (adj[k] & m))
    es = sum(1 for e in edges if e['source'] in m and e['target'] in m)
    print(f'{name:22} {len(m):6d} nodes {es:6d} edges   isolated within match: {100*iso/len(m):3.0f}%')

report('hasFindings',    lambda n: bool(n.get('rules')))
report('hasAdvisory',    lambda n: (n.get('advisories') or 0) > 0)
report('minReach>=5',    lambda n: (n.get('dependentRepos') or 0) >= 5)
report('kinds=action',   lambda n: n.get('kind') == 'action')
report('kinds=workflow', lambda n: n.get('kind') == 'workflow')
report('origins=seed',   lambda n: n.get('origin') == 'seed')
report('unanalyzedOnly', lambda n: not n.get('analyzed'))
PY
```

### Components and depth

Both need a traversal; do it in Python over the payload above rather than in a recursive CTE, which
is slower and easier to get wrong.

```bash
python3 - <<'PY'
import json, collections
g = json.load(open('/tmp/g-all.json'))
adj = collections.defaultdict(set)
for e in g['edges']:
    adj[e['source']].add(e['target'])
    adj[e['target']].add(e['source'])
keys = [n['key'] for n in g['nodes']]
seen, sizes = set(), []
for k in keys:
    if k in seen:
        continue
    stack, size = [k], 0
    seen.add(k)
    while stack:
        cur = stack.pop(); size += 1
        for nxt in adj[cur]:
            if nxt not in seen:
                seen.add(nxt); stack.append(nxt)
    sizes.append(size)
sizes.sort(reverse=True)
print(f'components {len(sizes)}  largest {sizes[0]}  '
      f'largest share {100*sizes[0]/len(keys):.1f}%  singletons {sum(1 for s in sizes if s == 1)}')
PY
```

Record the depth histogram of the dependency closure alongside it. **Transitive cost is exponential
in depth and linear in breadth**, so which of the two this corpus has decides whether to optimize
the traversal or the fan-out.

---

## Phase 2 — Cost

### Confirm whether statistics exist

```bash
sqlite3 -readonly "file:$DB?immutable=1" "SELECT count(*) FROM sqlite_master WHERE name='sqlite_stat1';"
```

Zero means SQLite is planning on defaults. `ANALYZE` runs only inside compaction, beside `VACUUM`.
Measure plans and timings **both** before and after `ANALYZE` on a writable copy, and never mix the
two sets.

### Extract the SQL, then read the plans

Do not keep copies of the queries in this skill — they drift from `internal/store` silently, and a
plan read from a stale copy is worse than no plan. Extract them per review:

```bash
mkdir -p /tmp/abom-review-sql
grep -n 'SELECT' internal/store/*.go | less        # locate the read paths worth the sweep
# then write each into its own .sql file, keeping the ? placeholders
scripts_dir=.github/skills/refine-abom-data-structures-and-algorithms/scripts
"$scripts_dir/query-plans.sh" "$DB" /tmp/abom-review-sql
```

Start with the heaviest known shapes: `graphNodesQuery` (four CTEs), the frontier rollup,
`AdvisoriesByReach`, the BOM component fill, and whatever the current review's timings put on top.

What to flag is tabulated in the skill body. The two that matter most: `SCAN` on the inner side of
a join, and `CORRELATED SCALAR SUBQUERY`.

### Time the paths a person waits on

```bash
make start                                   # resident; never time through a fresh process
for ep in summary graph/full remediation bom pinning frontier exposure advisories; do
  printf '%-14s ' "$ep"
  curl -sS -o /dev/null -w '%{time_total}s  %{size_download} bytes  HTTP %{http_code}\n' \
    "localhost:8099/api/$ep"
done
```

**Record the status code, always.** An error path is the fastest path in any system. The first
sweep logged `/api/bom` at 88 bytes in 1 ms and `/api/prune` at 92 bytes in 33 ms as fast
endpoints; both were **HTTP 500** and the bytes were the error message.

Run it twice and report cold and warm separately — the first pass populates the page cache, and a
single run silently reports one or the other depending on what ran before it.

For payload decomposition:

```bash
curl -sS "localhost:8099/api/graph/full" > /tmp/p.json
wc -c /tmp/p.json
gzip -c /tmp/p.json | wc -c
python3 -c "
import json,collections
g=json.load(open('/tmp/p.json'))
n=len(g['nodes'])
print('bytes/node', round(len(open('/tmp/p.json','rb').read())/n))
print(collections.Counter(e['kind'] for e in g['edges']).most_common())
"
```

**Quote compressed and uncompressed.** A proposal justified on the uncompressed figure predicts a
network saving that gzip already took.

### Traversal cost

`internal/graph` holds the in-memory work. State the complexity in terms of the census numbers,
then measure it — the two disagreeing is the finding.

```bash
go test ./internal/graph/... -run XXX -bench . -benchmem 2>&1 || \
  echo "no benchmarks yet — adding them is a legitimate review output, handed to codeql-abom-dev-test"
```

Count **calls and allocations, not elapsed time**, for anything asserted in a test. A duration
assertion is flaky and proves nothing; a call-count assertion catches N+1 and keeps catching it.

---

## Phase 3 — Growth

```bash
scripts_dir=.github/skills/refine-abom-data-structures-and-algorithms/scripts
"$scripts_dir/subsample-corpus.sh" "$DB" /tmp/abom-scale 10 25 50 100
```

At each size: rerun the phase 1 census and the phase 2 timings, then fit

$$\log(\text{cost}) = a + b \cdot \log(\text{size})$$

```bash
python3 - <<'PY'
import math
# sizes = repositories at each sample; costs = the measured seconds or bytes for one stage
sizes = [110, 275, 550, 1099]
costs = [0.0, 0.0, 0.0, 0.0]          # fill from the runs
xs = [math.log(s) for s in sizes]
ys = [math.log(c) for c in costs if c > 0]
if len(ys) == len(xs):
    n = len(xs); mx = sum(xs)/n; my = sum(ys)/n
    b = sum((x-mx)*(y-my) for x, y in zip(xs, ys)) / sum((x-mx)**2 for x in xs)
    print(f'exponent b = {b:.2f}')
    for target in (10_000, 100_000):
        print(f'  at {target:,} repos: {math.exp(sum(ys)/n + b*(math.log(target)-mx)):.2f} '
              f'(extrapolating {target/sizes[-1]:.0f}x beyond the largest measured point)')
PY
```

Report $b$ **with the points it was fitted from**, and state the extrapolation factor. Four points
across one order of magnitude, projected two orders out, is a weak claim and must be labeled one.

---

## Traps that invalidate a measurement

### A read-only open that fails on a good snapshot

`sqlite3 "file:$DB?mode=ro"` returns `unable to open database file (14)` against a fresh `.backup`.
The snapshot is fine: a WAL-mode database needs a `-shm` file, a read-only connection may not
create one, and `.backup` does not produce one. Against the *live* database the same command
succeeds, because the running server already made the `-shm`.

So the form that appears to work is the one that reaches the database you must not touch, and the
form that appears broken is the one that is correct. Use `immutable=1` everywhere — it works on
both, and it guarantees no write can reach the file, which `mode=ro` on a WAL database does not.

### The page-cache cliff

`DefaultCacheSizeKB` is 256 MiB. While the database fits inside it, every read is from memory and
every growth curve looks linear.

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
SELECT page_count*page_size/1048576.0 AS db_mib, 256.0 AS cache_mib,
       CASE WHEN page_count*page_size/1048576.0 > 256 THEN 'ABOVE CACHE' ELSE 'fits in cache' END
FROM pragma_page_count(), pragma_page_size();"
```

**Report this at every sample point**, and mark where the crossing falls. A curve measured entirely
below the cliff says nothing about life above it, and one organization's database can sit well below
the cliff while the scale the review is being asked about does not.

### Serialized reads

`MaxOpenConns(1)`: concurrent requests queue. Single-request timing cannot see it, and request
queueing is what organization scale meets before it meets query cost. If you have not measured
concurrently, **say that you have not** rather than reporting a latency as though it were a
throughput.

### Two durability modes

Ingest opens with `synchronous = OFF`, serving with `NORMAL`. Timings are not comparable across
them. Label every number with the mode that produced it.

### Tombstones

Measure through `abom_node_live` / `abom_edge_live`. On a pruned corpus the base tables hold rows
no read path ever returns, and timing them inflates every figure by an amount that varies with how
recently pruning ran.

### Process startup

Time against the resident server. A per-invocation harness has already been measured attributing
0.75s to work that takes 0.004s.

### Gzip

The Explorer payload is 8.0 MB raw and 473 KB gzipped. Quote both, or a reader will price a
transfer saving that does not exist.

### A crawl in flight

`make status` first, every time. Every `codeql` invocation shares one compilation cache and every
core, so a timing taken during discovery is not evidence — and it will not look wrong.

### A code path the corpus does not exercise

Before reporting anything as cheap, **count the rows in every table its predicate touches**. The
first sweep measured the ownership check at 0.00 s and nearly reported it as a proven non-issue;
`abom_owned_scope` holds zero rows, so the view behind it is empty and every `EXISTS` is false
immediately. Report that as **unmeasured**, and name the data state that would exercise it.

```bash
sqlite3 -readonly "file:$DB?immutable=1" "
SELECT 'owned_scope', count(*) FROM abom_owned_scope
UNION ALL SELECT 'owned_scope enabled', COALESCE(SUM(enabled),0) FROM abom_owned_scope
UNION ALL SELECT 'honored view', count(*) FROM abom_owned_scope_honored;"
```

### An index is not free

One writer, write-heavy ingest, 24 indexes already. Every proposed index states its write cost, and
whether it belongs in `CreateIndexes()` (after bulk load) or `Migrate()` (needed by a page).
