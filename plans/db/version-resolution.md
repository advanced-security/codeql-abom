# A commit-pinned action can still be named as a version

**Status:** The relation is built and the submitted purl uses it — **266 of the 314 commit-pinned
dependencies ABOM submits (85%)**. The tags API for what the crawl cannot reach is not built.

## Why this exists

GitHub generates Dependabot alerts for Actions that use **semantic versioning, not SHA
versioning**. It is the alerts that stop and not Dependabot — version updates support commit pins,
so a SHA-pinned repository still gets bump pull requests and loses only advisory matching — and it
is a property of the reference rather than of the pin's quality, since pinning is the recommended
hardening. The gap is that a commit carries no version to compare against a range.
So the better an organization's supply-chain hygiene, the less Dependabot alerting can tell it. Of
**31,871** live referencing edges, **10,881 (34%)** are pinned to a commit, and **9,668 of those
(89%)** carry a trailing `# v4.2.2` comment naming the version somebody believed they were pinning.

## Pending

1. **The tags API for the rest.** **523 of the 1,111** distinct (package, commit) pins are not
   recoverable from the queue, because nothing has analyzed that action at a tag pointing to that
   commit. One listing per repository, cached in the same relation with `source = 'tags-api'`.
2. **A comment that disagrees with the tag is worth reporting.** Once both are recorded, a pin
   whose comment names a version the commit is not is either a typo or a lie, and the interface can
   say which references are honestly labelled. Deliberately after 1.

## The constraint that must not be broken

**A version submitted on a claim is a reference nothing verified.** Three sources: `crawl` is a tag
this tool watched resolve to a commit, `tags-api` is one read back from GitHub, and `comment` is
what somebody *intended*. Only measurements may name a version for submission; a claim never
displaces a measurement, and a measurement always promotes a claim. A snapshot naming a real
manifest *replaces* GitHub's own analysis, so submitting `4.2.2` on a comment's word puts a
dependency the repository does not have in front of Dependabot.

**Unique on `(owner, repo, tag, commit_sha)`, never on the tag.** A major-version tag points
somewhere new after every release, and dropping the previous reading leaves an old purl
unexplainable.

## The open fork

**Most commit-pinned references are direct, and ABOM does not submit them.** They are omitted
because "GitHub finds those" — but GitHub finds them *as commits*, so its own analysis cannot alert
either. The split between direct and indirect is what sizes this fork, and it is owed a measurement
against a declared organization before either branch is chosen. The ratio carried here previously
described a corpus that has been retired.

- Submit at the **synthetic** manifest path, and a direct dependency sits beside GitHub's own
  reading of `.github/workflows/ci.yml` rather than replacing it: two entries for one dependency.
- Submit at the **real** manifest path, and it replaces GitHub's analysis of that workflow, the
  irreversible move `api/dependency-submission.md` exists to prevent.

Worth an experiment on `codeql-abom-test1` first: submit one direct dependency as a version at the
synthetic path and read back whether the graph shows one entry or two.

## Verification

- The invariants are proved by mutation, each failing only its own test, and mutated **separately**:
  removing the source filter masks the displacement bug, since the lookup returns the claimed row.
- The backfill is idempotent: a second run over the same corpus adds nothing.
- Every pass runs against a **copy**, and the live corpus gaining nothing is the correct outcome.
- A `DISTINCT` over more columns than the thing being counted is not a count of that thing:
  `pin_comment` inside the `DISTINCT` once inflated 1,111 pins to 1,333.
