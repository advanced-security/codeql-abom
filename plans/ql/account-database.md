# One database for the account, built from what the crawl already fetched

**Status:** Blocked on a measurement. [`ql/uses-resolution.md`](uses-resolution.md) decides whether
a `uses:` resolves across repositories in one database; nothing here is worth building until it
has run.

## Why this exists

Two things a database per repository cannot do.

**Dataflow stops at every `uses:`.** The callee is not in the database, so the call resolves to
nothing. Everything in [`ql/semantic-model.md`](semantic-model.md) that is worth having — a
reference resolved through an environment variable, a reference built from a remote source — needs
the callee's source beside the caller's.

**A whole-corpus projection has to be stitched.** A `@kind graph` query answers per database, so
one picture of the account is N result sets joined afterwards by a tool that has to re-derive what
CodeQL already knew.

**The cost argument is weaker than it looks, and is not the reason.**
[`db/analysis-ledger.md`](../db/analysis-ledger.md) measured the database build at **20.3% of a
repository's cost**, so collapsing many builds into one saves a fifth of expand rather than the
bill. Separately, [`api/org-enumeration.md`](../api/org-enumeration.md)'s remaining per-repository
read is the **code-scanning probe**, which this changes not at all. Building this for speed would
be building it for the wrong reason.

**This is the workaround for an extractor that cannot do overlays yet.** `actions` declares no
`overlay_support_version`, so `--overlay-base` is accepted, exits 0 and does nothing;
`TestTheActionsExtractorStillCannotBuildAnOverlayBase` fails when that changes. Overlays are the
supported way to get one database's reach at incremental cost, and when they arrive most of this
plan retires. It shares that test.

## Pending

1. **Nest the checkout layout.** `expand.go` joins with an underscore —
   `${WorkDir}/${owner}_${repo}${refSuffix}` — which cannot be split back: `foo_bar_baz` is both
   `foo/bar_baz` and `foo_bar/baz`, and repository names contain underscores. Nothing can attribute
   a result to a repository by path until this is a nested `${owner}/${repo}@.../`. Useful on its
   own, and depends on nothing.
2. **Lay the tree out by resolved commit, not by reference.** `abom_ref_resolution` already maps a
   reference to the commit it pointed at, and `run_key` already hashes `db_commit` with **no
   reference in it**, because two references at one commit are one evaluation. A tree keyed by
   commit therefore collapses `@v4` and `@v4.2.2` into one extraction while the frontier stays keyed
   `(node_key, ref)`. References remain the unit of work; commits become the unit of extraction.
   **132 of 225** dependencies are analyzed at more than one reference, which is what sizes the
   saving. Depends on 1, and on `uses-resolution` item 2 — if the resolver can only be pointed at a
   reference by directory name, this is the wrong layout and the plan takes the duplication instead.
3. **Retain what is fetched.** Checkouts and databases are removed after each unit unless
   `KeepArtifacts` is set. An account database needs every tree present at once, so retention
   becomes a mode with a stated disk cost rather than a debugging flag. Depends on 2.
4. **An identity for a database that is not one repository.** `run_key` hashes a single
   `repository` and `db_commit`, and this database has neither. It needs a manifest — the set of
   `(repository, commit)` pairs extracted — hashed into the run key in their place. Depends on 3.
5. **Attribute an ingested row to a repository.** `ingest.Options.Repository` is required, and
   every key `normalize` builds embeds the repository name, so nothing derives it from a path
   today. One resolver, from source path to repository, exercised by the fact that keys built this
   way are byte-identical to the keys the per-repository path already produces. Depends on 1 and 4.
6. **One database per language over one tree.** The tree is extracted once per language rather than
   once overall, which is what lets [`ql/semantic-model.md`](semantic-model.md) add an adapter in
   another language without disturbing `actions`. Depends on 5.

## The constraints that must not be broken

**The account database must never become the discovery engine.** Resumability is the property that
a run dying halfway does not repeat finished work, and it holds because the queue and the ledger
are per `(dependency, reference)`. A newly discovered hop that forces a whole-corpus rebuild
destroys it. Per-repository databases stay; this is a periodic artifact that **reads**, in the same
class as `report`, `bom` and `submit`.

**A content hash of a database directory must never enter `run_key`.** Carried unchanged from
[`db/analysis-ledger.md`](../db/analysis-ledger.md): a CodeQL database is not byte-reproducible, so
a content hash makes every rebuild invalidate every row, the skip silently stops firing, and
nothing looks broken. The manifest hashes *inputs* — the repositories and commits that went in.

**`abom_database_catalog` is not this, and may not be reused for it.** That table answers whether
code scanning is enabled on a repository — a fact about configuration, which moves independently of
any commit. What this plan needs is a record of what was extracted. They are the Enablement and
Coverage questions in [`docs/vocabulary.md`](../../docs/vocabulary.md), and that file already
refuses to let them be synonyms.

**A validator for content is not a validator for configuration.** `If-None-Match` proves a listing
did not change; it proves nothing about somebody toggling code scanning. Nothing may cache an
enablement answer against a commit.

**Extraction over third-party checkouts is safe here for a reason that does not generalize.**
`actions` extraction parses YAML and runs no build, so nothing in a fetched repository executes.
The moment a second language with a build step is added, that stops being true.

## Verification

- Keys produced through the account database are byte-identical to the keys the per-repository path
  produces for the same repository and commit. Any divergence is named, not reconciled.
- Node, edge, reach, exposure, bill-of-materials component and pinning totals are unchanged.
  Snapshot with `sqlite3 abom.db ".backup .abom/before.db"` — `cp` does not read the WAL.
- The disk cost of retention is measured against the crawled organization before retention becomes
  a default, because it is the objection this plan has no answer to yet.
- Extraction and evaluation time for the whole tree, against the sum of the per-repository times it
  replaces. A saving smaller than 20.3% of expand means the measurement in *Why this exists* was
  optimistic, and that belongs in `plans/implemented.md`.
