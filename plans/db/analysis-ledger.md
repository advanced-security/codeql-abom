# A verifiable analysis ledger

**Status:** Every run records the pack, its content digest and the CodeQL that evaluated it, and
work already on record is not done twice. Two items remain, both blocked outside this repository.

## Why this exists

Every unit of analysis work is **(database identity x query identity)**, and both are
content-addressed: if the pair is already on record, CodeQL is deterministic and nothing needs to
run. The ledger is worth exactly what its columns are worth — a row that omits what evaluated it
cannot be told from one produced by a different CodeQL, and the skip that reads it trusts it.

## Pending

1. **Tighten the identity columns to `NOT NULL`.** Blocked by data: **1,555 of 5,472** ledger rows
   predate the identity columns, and a pre-identity row never matches a current key, so each of
   those pairs has to be analyzed once more before the constraint can hold. A one-line change
   after a full re-crawl, and wrong before one.
2. **The database digest and overlay databases**, together: a digest's only job is asserting that
   a stored base is still the one we analyzed, and nothing stores a base until overlays do.
   Blocked by the extractor — `actions` declares no `overlay_support_version`, so `--overlay-base`
   is accepted, exits 0 and is a silent no-op.
   `TestTheActionsExtractorStillCannotBuildAnOverlayBase` fails when that changes, and the
   database build is **20.3%** of a repository's cost.

## The constraint that must not be broken

**A content hash of a database directory must never enter `run_key`.** A CodeQL database is not
byte-reproducible, so the run key hashes *inputs* — `H(repo, commit, cli_version, flags)`. Put a
content hash in it and every rebuild invalidates every row it touches, the skip silently stops
firing, and nothing looks broken: the crawl quietly costs what it did before the ledger existed. A
result hash is repository-independent for the same reason, so no absolute path, timestamp, run id
or `automationDetails` may enter one.

## Verification

- Two consecutive full rebuilds produce identical `run_key` sets and identical result hashes.
- An unchanged corpus evaluates zero queries and changes no counts.
- Deferred with the overlays: two builds of the *same commit* produce **different** database
  digests and the **same** run keys, which fails loudly in the one case that would otherwise
  retire the skip in silence.
