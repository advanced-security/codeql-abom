# A commit-pinned action can still be named as a version

**Status:** measured crawl mappings feed submission versions; the tags-API lookup is not built.

## Why this exists

Immutable execution identity and semantic-version context are different facts, and ABOM must keep
both. Native Actions locks select commits while retaining symbolic refs; this is the resolution
baseline in [actions-lockfile.md](actions-lockfile.md), not an optional source behind a current tag
lookup. Bare SHAs carry no release version to compare against an advisory range, while a tag or pin
comment alone cannot prove which code was selected.

The existing measured relation and PURL normalization remain useful. Native lock metadata supplies
contextual evidence, but parsing its `ref` does not verify that it names a semantic release at the
locked commit. Further lookup should fill that evidence gap without changing the selection. Native
Dependabot updates and automatic advisory/DG ingestion are different capabilities, measured in
[dependency-graph-crosscheck.md](../api/dependency-graph-crosscheck.md).

## Pending

1. **The tags API for remaining version gaps.** First count distinct selected package/commit pins
  missing usable measured version evidence after crawl and native-lock provenance are considered.
  Work on a copy of the declared organization's current data. List tags
  once per repository per refresh, paginated and cached in the existing relation with
  `source = 'tags-api'`. Resolve annotated tags to commits where necessary. API errors, exhausted
  budgets and incomplete pagination remain distinct from a complete listing with no matching tag.
  A page cap cannot silently establish absence. Cross-host lookup consumes
  [github-hosts.md](../api/github-hosts.md).
2. **A comment that disagrees with the tag is worth reporting.** Once both are recorded, a pin
  whose comment disagrees with the measured version is a discrepancy to show, not a reason to
  infer intent or replace the pin. Depends on 1.
3. **Choose an export version deterministically without discarding evidence.** Several semantic
  tags may point to one SHA. Define and test specificity and tie-breaking, preserve all measured
  tag/commit pairs and their sources, and keep tag observations distinct from branches with
  version-like names. A moved tag adds a new observation rather than erasing the old one. Reuse
  `normalize.PackageURL` and its measured encoding; the raw ref is unchanged. Depends on 1.
4. **Look upstream only through recorded provenance.** Where
  [fork-provenance.md](fork-provenance.md) identifies a parent or mirror candidate, an upstream tag
  may name the same measured SHA. Attribute the version to the repository where it was observed;
  do not assign the fork's tag to the parent just because the names match. A configured mapping
  or a pin comment is insufficient. Depends on that plan's identity collection and 1 through 3,
  not on completing its export integration.
5. **Use lock evidence without turning it into a live-tag reading.** Consume the contextual
    selection and verification provenance from [actions-lockfile.md](actions-lockfile.md). Preserve
    the original requested ref, lock symbolic ref and selected digest. A branch or partial version
    is not an exact release, and a parsed lock is not an independent ref/commit measurement.
    Attribute verified release evidence to its producer, repository and time; a later tag reading
    cannot rewrite the historical locked choice. Depends on that plan's evidence contract, not on
    a new publisher.

## The constraint that must not be broken

**A version submitted on a claim is a reference nothing verified.** Three sources: `crawl` is a tag
this tool watched resolve to a commit, `tags-api` is one read back from GitHub, and `comment` is
what somebody *intended*. Only measurements may name a version for submission; a claim never
displaces a measurement, and a measurement always promotes a claim. A snapshot naming a real
manifest *replaces* GitHub's own analysis, so submitting `4.2.2` on a comment's word puts a
dependency the repository does not have in front of Dependabot.

**Unique on `(owner, repo, tag, commit_sha)`, never on the tag.** A major-version tag points
somewhere new after every release, and dropping the previous reading leaves an old purl
unexplainable. Host becomes part of repository identity before cross-host measurements are admitted;
same-named repositories on two hosts cannot share a tag cache or version observation.

**Selection is not a source-priority contest.** A lock chooses a commit for a particular consumer;
the tags API says where a tag points at a measurement time. Neither a higher-priority source nor a
newer timestamp may substitute live code for a locked workflow. Contextual selection is not another
global `ResolutionLockfile` entry in `SHAForTag`.

## Direct pins

**Direct-pin publication and upstream aliases remain deferred.** Native locks preserve the version
and commit relationship without ABOM inventing extra packages. Determine what the native products
consume before considering a measured-version supplement. If a residual publication gap is proven,
any experiment follows [dependency-submission.md](../api/dependency-submission.md) on an approved
target. Never claim a real workflow manifest, relabel indirect use as direct to influence alerting,
or infer better coverage from duplicate SHA/version packages. Adding version evidence grants no
permission to expand submission.

## Verification

- The invariants are proved by mutation, each failing only its own test, and mutated **separately**:
  removing the source filter masks the displacement bug, since the lookup returns the claimed row.
- The backfill is idempotent: a second run over the same data adds nothing.
- Every pass runs against a SQLite **backup**; the live database remains untouched.
- A `DISTINCT` over more columns than the thing being counted is not a count of that thing:
  `pin_comment` inside the `DISTINCT` once inflated 1,111 pins to 1,333.
- Tests cover multiple tags, moved tags, annotated tags, version-like branches, unmatched SHAs,
  interrupted pagination, permission/rate failures and parent/fork tags with equal names but
  different commits. Assert request counts and reuse across pins in the same repository.
- The graph, raw refs, finding counts and direct/transitive classification remain unchanged after
  enrichment. Export versions have measured provenance and deterministic selection.
- Two consumers locking the same tag differently retain different selections. A moved live tag,
  forged lock ref, branch-valued lock ref and unavailable verification do not become an invented
  exact version or alter the code analyzed. Test native and crawl evidence without assuming one
  global tag/commit choice.

## Prior art

Jesse Houwing's
[ForkResolver](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/fork-resolver.ts)
and [version-selection tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/fork-resolver.test.ts)
provide useful SHA-to-version and parent-lookup examples from
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission).
ABOM already has the relation and PURL normalization; the lesson is broader measured lookup and
explicit selection tests, not replacing its evidence or copying a silent pagination ceiling.

GitHub's [native lock format](https://github.com/github/actions-lockfile/blob/go/v0.0.4/README.md)
now supplies the default context for selection. Use its parser and retain verification provenance;
the presence of both `ref` and `commit` is not, alone, a vulnerability-applicability verdict.
