# One database for the account, built from what the crawl already fetched

**Status:** Unblocked, and narrower than its own opening argument below. Cross-repository `uses:`
resolution is measured in `plans/implemented.md`: it works, but only when a callee's checkout sits
at exactly `${owner}/${repo}/`, with no reference or commit anywhere in the path. One of the two
reasons this plan gives for existing turns out not to need it at all — see the revised paragraph.

## Why this exists

One thing a database per repository cannot do, and one it turns out not to need this plan for.

**A whole-corpus projection has to be stitched.** A `@kind graph` query answers per database, so
one picture of the account is N result sets joined afterwards by a tool that has to re-derive what
CodeQL already knew. This is the reason that still holds.

**Dataflow crossing a `uses:` does not need this plan for the first hop.** The callee only has to
sit in *a* database, not in *this* one. `codeql/actions-all` strips a literal
`.github/actions/external/` (composite action) or `.github/workflows/external/` (reusable
workflow) segment from a callee's path before matching it against the caller's `uses:` string, so a
repository's own, ordinary database already resolves and taints a callee staged under its **own**
`.github/actions/external/${owner}/${repo}/` — measured in `plans/implemented.md`, with no second
repository checked out anywhere. What [`ql/semantic-model.md`](semantic-model.md) needs from
dataflow for a first-hop reference is reachable per repository this way. Whether fetching a
repository's immediate `uses:` targets into that convention path is worth a plan of its own is
open, and is not decided here.

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
   a result to a repository by path until this is nested. Useful on its own, and depends on
   nothing.

   **The nested form has to be bare `${owner}/${repo}/`, never `${owner}/${repo}@.../`, or
   resolution never fires — for any repository, not only a colliding one.** `getResolvedPath()`
   matches a caller's ref-stripped `getCallee()` against a callee's own *unstripped* checkout path;
   measured directly in `plans/implemented.md`, a suffixed directory resolves to nothing even when
   it is the only checkout of that repository anywhere in the tree. A repository can be laid out
   bare only where the tree never needs a second checkout of it.

   **Extraction has its own gap here, independent of naming.** The `actions` extractor's default
   `LGTM_INDEX_FILTERS` finds `.github/workflows/*.yml` only at the source root, not recursively,
   while `**/action.yml` already is recursive. A nested repository's workflow files are silently
   skipped unless the tree is built with an additional recursive include appended to
   `LGTM_INDEX_FILTERS`. Not a defect in anything ABOM ships today — `internal/expand/checkout.go`
   always places `.github/workflows` at the checkout root — but a fact whoever builds this tree
   needs before a thin result set looks like a resolution failure instead of a filter gap.
2. **Lay the tree out by resolved commit, not by reference.** `abom_ref_resolution` already maps a
   reference to the commit it pointed at, and `run_key` already hashes `db_commit` with **no
   reference in it**, because two references at one commit are one evaluation. A tree keyed by
   commit therefore collapses `@v4` and `@v4.2.2` into one extraction while the frontier stays keyed
   `(node_key, ref)`. References remain the unit of work; commits become the unit of extraction.
   **132 of 225** dependencies are analyzed at more than one reference, which is what sizes the
   saving. Depends on 1.

   **This saves extraction cost only, and never restores resolution.** A commit-keyed directory
   still carries a segment beyond `${owner}/${repo}`, so it resolves no better than a
   reference-keyed one — collapsing `@v4` and `@v4.2.2` into one checkout avoids extracting the
   same commit twice; it does not make either reference's `uses:` resolve. The 132 stay unresolved
   under either keying; only the 93 needing exactly one checkout can ever be laid out bare.
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

**A directory name is not a substitute for reading a reference.** The bare `${owner}/${repo}/`
layout that lets a `uses:` resolve works only because it happens to equal the ref-stripped callee
string byte-for-byte — measured down to a lone, non-competing suffixed directory resolving to
nothing (`plans/implemented.md`). Nothing here teaches CodeQL to read a reference or a commit from
a path segment, and nothing built on top of this plan may assume otherwise.

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
- A bare-named, single-reference repository's callee resolves and taints its `run:` step; a
  suffixed one does not. The account database repeats the spike's fixture in
  `plans/implemented.md` at corpus scale rather than taking a two-repository measurement on faith.
