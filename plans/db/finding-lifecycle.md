# A finding that stops being reported is never removed

**Status:** Sightings are recorded and the read-only sweep reads them. The tombstone is not built,
and waits on a crawl that has produced sightings for the report to be believable.

## Why this exists

`abom_node` and `abom_edge` carry `removed_at` and `removed_generation`. `abom_finding` carries
neither, so it only ever grows. Findings accumulate reporting a fingerprint no current analysis
reports anywhere in their repository, and others naming a file no current finding mentions at all.
The workflow changed; the row did not. How many of each is owed a measurement against a declared
organization; the figures carried here previously described a corpus that has been retired.

Every count drawn from `abom_finding` includes them, undivided and unmarked — `pinning_coverage`
reads `actions/unpinned-tag` directly, the bill of materials counts findings per component, and
severity x reach scores a node by findings attached to it. None of those is wrong about what it
read. They are wrong about *when*, and the error runs one way only: a finding that was fixed still
counts, so every figure reads as more risk than there is, never less.

## Pending

1. **The tombstone column and the `_live` view.** An afternoon of work, and deliberately last. It
   waits on a crawl that has produced sightings: with none recorded, the sweep condemns nothing,
   which is the correct first answer and not a clean bill. Nothing marks anything until the report
   is believable.
2. **State what it moved.** After the first re-crawl and sweep, `pinning_coverage` changes, and the
   number it changes by is stated rather than discovered later.

## The constraint that must not be broken

**Absence in a run that could not have reported it is not absence.** Staleness is decided from
sightings — for a repository, take the newest run of the owning suite at each commit still tracked,
and a finding is stale when none of them sighted it. A sweep that reads "no sighting" as "not
reported" tombstones the entire table on first run; one that condemns on the evidence of a run that
attributed nothing at all condemns findings that are still being reported. Priced against the
corpus before any code, the obvious rule — the newest owning-suite run at any reference does not
name it — condemned **2,420** findings that another reference legitimately reports.

The failure mode is silent and one-directional: every downstream count then reads as *safer* than
the truth. Every other invariant in this repository guards against inventing risk; this one guards
against inventing safety, which is worse.

## Verification

- A corpus with no sightings recorded yet tombstones **nothing at all**, rather than everything.
- A finding whose workflow line was deleted stops being counted after the repository is
  re-analyzed, and its row is still readable with the generation that removed it.
- A repository analyzed only by the graph suite loses no findings.
- A finding carried only by an older reference is not tombstoned by a newer reference's run.
- A commit settled through content equivalence tombstones nothing: it has no run of its own to
  consult and contributes no evidence either way.
- Staleness is only ever observable across a change in run identity. Re-running an unchanged commit
  with an unchanged pack resolves to the *same* run row and is no new evidence, so a fixture that
  lets two runs collapse proves nothing and must be written as a pack upgrade.
