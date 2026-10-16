# Workflows without an Actions lockfile

`.github/workflows/actions.lock` records, for every action a repository runs, the exact commit that
will run. A repository with workflows but no lockfile has no such record: each `uses:` reference
resolves to whatever its ref points at on the day the workflow runs, and a tag can be moved to a
different commit at any time by whoever owns it.

This query reports that absence once per repository, naming how many workflow files are affected.

## Why this is reported separately

`abom/actions/uncovered-action-ref` compares a workflow against a lockfile, so it has nothing to
compare against — and therefore stays silent — on exactly the repositories with the least coverage.
Reading that silence as "no problem found" inverts the truth. The two queries are complements: one
measures gaps *within* an adopted lockfile, this one measures *non-adoption*.

Together they hold the invariant that every repository with workflows produces at least one finding
until it is both onboarded and fully covered.

## Scope

Only files directly in `.github/workflows/` ending `.yml` or `.yaml` are counted. Composite actions
(`.github/actions/*/action.yml`) are not workflows, and GitHub does not run workflows from
subdirectories of `.github/workflows/`, so neither is counted.

A repository with no workflow files at all is not reported: there is nothing for a lockfile to
cover, so its absence is not a gap.

## Recommendation

Run `gh actions-lock` to generate the lockfile and commit it, then `gh actions-lock --verify-local
--no-fix` in CI to keep it honest.

## Example

A repository containing only this workflow, and no `.github/workflows/actions.lock`, is reported:

```yaml
on: push

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      # Resolves to whatever `v4` points at when this runs. Nothing here records what that was.
      - uses: actions/checkout@v4
```

## References

- GitHub Docs: [Workflow dependency lockfiles](https://docs.github.com/en/actions/concepts/security/workflow-dependency-lockfiles)
- GitHub Docs: [Locking action dependencies in a repository's workflows](https://docs.github.com/en/actions/how-tos/secure-your-work/lock-workflow-dependencies)
- Common Weakness Enumeration: [CWE-829](https://cwe.mitre.org/data/definitions/829.html)
