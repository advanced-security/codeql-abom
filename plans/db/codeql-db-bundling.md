# A retained database is an archive, not a directory

**Status:** Deferred, and measured. `--database-cache-dir` retains each built database as a plain
directory at `${CacheDir}/${owner}/${repo}/${sha}-${filter}-${mode}/db`, indexed by
`abom_local_database` and bounded by a 25 GiB ceiling. Two thirds of what that ceiling holds is
build logs no query reads. `codeql database bundle` was measured and does far better than stripping
them, but adopting it moves the cache from pure filesystem work to something that invokes CodeQL on
every store and every use, so it waits.

## Why this exists

Measured 2026-09-22 on the crawled organization, across the 17 databases a real crawl retained.
What a database is made of barely varies:

| Component | Range | Read when a query runs? |
| --- | --- | --- |
| `log/` | 13,476–16,640 KB | No |
| `db-actions/` | 4,968–6,440 KB | **Yes** |
| `results/` | 1,392–1,536 KB | No |
| `src.zip` | ~150 KB | Yes |

The mean retained database is **18.6 MB** and the spread is only 17.9–20.2 MB. That tightness was
first read as CodeQL's fixed per-database overhead, which was the wrong diagnosis: it is the build
log, near-constant whatever the repository, so `actions/github-script` costs the same as
`juice-shop`. The part a query actually needs is about **5.5 MB**.

`codeql database bundle` already excludes logs, results, TRAP and temp unless asked for them.
Measured on `actions/setup-node`:

- 19.4 MB of directory becomes a **264 KB** archive — **75×** — in 2.7 s.
- `codeql database unbundle` restores it in 1.24 s to 5.36 MB, holding the dataset and the source.
- The source database is not consumed, though bundling writes a small metadata file into it.
- ABOM's own `AbomEdges.ql` returns **54 rows, byte-for-byte identical** on the original and on the
  restored copy. A restored database answers the same questions; it is not the same bytes.

At 264 KB the 25 GiB ceiling holds on the order of **99,000** archives, and the whole 1,124
repository corpus is about **290 MB** rather than 21 GB. The disk ceiling stops being the thing
that decides how much of the organization can be kept, which is the constraint the current design
is shaped around.

What argues for waiting is the other side of the trade. Restoring costs **1.24 s** while the query
that follows it evaluated in **387 ms**, so a restore is roughly three times the cost of the
question it enables. A sweep over a thousand databases would spend about twenty minutes unbundling
before counting anything — and how that compares against simply rebuilding has not been measured,
because `codeql database create` was never timed on its own. Adopting the archive without that
number would be trading a disk problem for a latency problem on the strength of an assumption.

**Whether the restore could be skipped entirely was investigated and it cannot.** `codeql database
bundle` writes a `.dbinfo` into the archive and a `working/` directory beside the source, which
looks like it might be enough for the query commands to read the archive in place. It is not:
against CodeQL 2.27.0, `codeql database run-queries`, `codeql database analyze` and `codeql resolve
database` each refuse a `.zip` with `Database root ... is not a directory`. The only `bundle` any of
their help texts mentions is a bundle of *QL packs* carrying a `.codeqlmanifest.json`, which is an
unrelated thing. So the 1.24 s is unavoidable per use, and it is what item 6 exists to amortize.
Worth re-checking on a CLI upgrade, since this is a capability that could arrive without notice.

## Pending

1. **Stop retaining what nothing reads.** `log/` and `results/` are two thirds of every retained
   database and no query touches either. Removing them after the analysis lane has finished with
   them is a few lines, needs no CodeQL round trip and no format change, and is worth taking
   whether or not the archive is ever adopted. Depends on nothing.
2. **Time an extraction against a restore, on the same repository.** The one number this decision
   turns on and the one nobody has. Until it exists, "restoring is cheaper than rebuilding" is an
   assumption. Depends on nothing.
3. **Store an archive and its digest.** `bytes` becomes the archive's, and a `digest` column
   carries the content hash. Bundle the staged build *before* promotion, so the metadata file
   bundling writes lands in a directory that is about to be discarded rather than in the retained
   artifact. Depends on 2.
4. **Verify the digest before unbundling, and say so when it fails.** An archive that does not
   match what was recorded is a stated refusal that falls back to rebuilding, never a silent
   rebuild and never an unbundle attempted anyway. Depends on 3.
5. **Bundling belongs on the runner.** `internal/expand.DatabaseCache` is filesystem work today and
   must stay that way; `internal/runner.Runner` is what speaks to the CLI. The cache owns paths,
   digests and eviction, and is handed an archive. Depends on 3.
6. **A bounded working set of restored databases.** Without one, a repeated sweep pays 1.24 s per
   database every time it runs, and the investigation above establishes that the restore cannot be
   avoided. Two ceilings, not one: the archive store is cheap and large, the working set is
   expensive and small. Depends on 2, and on 2 showing that restore is worth avoiding.

## The constraints that must not be broken

**A restored database is functionally identical, never byte-identical.** Bundling drops the logs
and the results directory by design. Nothing may compare directory sizes, file counts or hashes of
the restored tree against the original and read a difference as corruption — the evidence that a
restore is sound is that a query returns the same rows, which is what was measured.

**A digest that does not match is reported, not worked around.** The point of recording it is to
catch a truncated or altered archive. Falling back to a rebuild is the right repair; doing so
without saying which archive failed turns a corruption signal into an unexplained slowdown.

**Never bundle with `--include-logs` or `--include-results`.** They are exactly what makes
retention expensive, and nothing reads either back. `--include-temp`, `--no-keep-temporary` and
`--cache-cleanup` are marked wizards-only and the CLI's own help warns that the temp directory
"may be required to run queries in certain scenarios" — none of them may be used on the strength
of a size saving alone.

**A bundle is not a database, and restoring it is not an optimization anybody may skip.** Measured
against CodeQL 2.27.0: every command that takes a database root refuses an archive. A design that
assumes an archive can be queried in place — or a working set sized as though restores were free —
is built on a capability that does not exist.

**The archive ceiling and the working-set ceiling are different budgets.** Collapsing them would
let a large working set evict archives, which is the expensive direction: an evicted archive costs
a full extraction, an evicted restore costs 1.24 s.

**Identity does not change.** An archive is still addressed by repository, commit, source filter
and analysis mode — the tuple `abom_query_run` hashes. Bundling changes how a database is stored,
never which database it is.

## Verification

- A database restored from its archive answers a real ABOM query with results identical to the
  original's. Measured once for `AbomEdges.ql` on `actions/setup-node` at 54 rows; the check
  belongs in a test rather than in a terminal.
- Extraction and restore are timed on the same repository, and the ratio is reported rather than
  assumed.
- The corpus total is measured as archives and compared against the 21 GB the raw form implies.
- A deliberately corrupted archive is refused by name, and the repair is a rebuild that says why.
- Re-checked on each CLI upgrade: whether a database command has gained the ability to read an
  archive in place, which would remove item 6 entirely.
- Stripping logs and results leaves every query a retained database could answer before still
  answerable, checked by re-running a suite rather than by reasoning about what reads them.
