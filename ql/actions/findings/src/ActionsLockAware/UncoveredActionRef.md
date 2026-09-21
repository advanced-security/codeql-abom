# Action reference not covered by the Actions lockfile

A repository that keeps `.github/workflows/actions.lock` has committed to recording, for every
action it runs, the exact commit that will run. A `uses:` reference the lockfile does not account
for is outside that record: it resolves to whatever its ref points at on the day the workflow runs,
and nothing in the repository says what that was.

This query reports such references. It only runs where a lockfile already exists — a repository
that has not adopted `gh actions-lock` is a different question, and not this one.

## Why this is not `actions/unpinned-tag`

`actions/unpinned-tag` asks whether a reference is *pinned*. This query asks whether the lockfile
*covers* it. The two diverge in three places, and the gap only ever falls this way:

- **Trusted owners.** `actions/unpinned-tag` exempts the `actions` and `github` organizations
  outright, so `actions/setup-go@v7.0.0` is never examined, lockfile or not.
- **Bare commits.** A reference written as a full SHA satisfies that query whatever the lockfile
  says, so an entry can be dropped from the lock without anything reporting it.
- **Stale entries.** A lockfile entry left at `v1.0.0` after the workflow moved to `v2.0.0`
  describes a commit nothing will run. The reference still looks pinned.

## Recommendation

Run `gh actions-lock` to add the missing entry, or `gh actions-lock --verify-local --no-fix` to
report every uncovered reference without modifying anything.

## Example

With a lockfile present, the second step is reported and the first is not:

```yaml
steps:
  # Covered: the lockfile lists this reference and resolves it to a commit.
  - uses: third/party@v1.0.0
  # Reported: a trusted owner, and absent from the lockfile.
  - uses: actions/setup-go@v7.0.0
```

Local (`./`), same-repository (`$/`) and container (`docker://`) references are never reported.
The first two resolve from the checkout at the commit already running, and `gh actions-lock`
records neither them nor images.

## References

- GitHub Docs: [Using an actions lockfile](https://gh.io/actions-lockfile)
- Common Weakness Enumeration: [CWE-829](https://cwe.mitre.org/data/definitions/829.html)
