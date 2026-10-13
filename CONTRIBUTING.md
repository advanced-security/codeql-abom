# Contributing

[fork]: https://github.com/advanced-security/codeql-abom/fork
[pr]: https://github.com/advanced-security/codeql-abom/compare

Thanks for taking the time to contribute.

Contributions to this project are
[released](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service#6-contributions-under-repository-license)
to the public under the [project's license](LICENSE).

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md).
By participating in this project you agree to abide by its terms.

## Before you start

```bash
make verify
```

That is the same command CI runs, so if it passes locally it should pass on the pull request. It
needs everything the [README's requirements](README.md#requirements) list, plus Python 3, Node,
Playwright's Chromium, `golangci-lint`, the `gh actions-lock` extension and a running Docker
daemon.

`.github/workflows/copilot-setup-steps.yml` installs all of it, and is a working reference if you
would rather not read a list.

## What this codebase cares about

Most of these were learned from a real bug here, which is why they read as rules rather than
preferences.

**A verdict belongs to a CodeQL rule, never to this code.** There is no `pin_status` column and
there must never be one. `TestSchemaHasNoVerdictColumns` enforces it. If you find yourself
inferring "safe" or "vulnerable" from something other than a rule ID, stop.

**Advisories enrich, they never adjudicate.** An advisory is stored as an attributed external
statement. Reporting "this action is vulnerable" from an advisory alone is wrong, because the
observed reference may sit outside the vulnerable range.

**Seed, upstream and owned are three different things.** How a repository entered the corpus, an
account holding something analyzed, and an account someone can actually change. Conflating any
two has produced a wrong number here before.

**Direct and transitive are different work.** One is a version bump you can make; the other waits
on someone else. Never collapse them into a single "affected" count.

**Priority is severity, then reach.** CVSS and EPSS are reported alongside and stay out of the
order, so it remains explainable.

**Say what is not known.** Coverage and unresolved dependencies are output, not omissions.

## Tests

Write the test first, watch it fail for the reason you expect, then implement.

Name a test after the behavior it protects, and put the *reason* that behavior matters in a
comment above it. That comment is what tells a future reader whether a failure is a regression or
an intentional change.

```go
// A retracted advisory must not be reported: its own publisher no longer stands behind it.
func TestWithdrawnAdvisoriesAreNotReported(t *testing.T) {
```

Some tests exist to stop the architecture eroding rather than to test a function. They are listed
in `.github/instructions/go-tests.instructions.md`; do not weaken them without saying why.

**Unit tests are not enough on their own.** Several architectural bugs here passed every test.
Where a change affects reported figures, snapshot a real database first and diff the headline
numbers afterwards. Without an organization of your own, `make corpus` builds a populated database
from the checked-in fixtures.

## Style

American English, in code and prose. `misspell` enforces it.

Comments state what the code cannot show, in about one line. Explain why a constraint exists;
never restate what the next statement does.

```go
// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
if err := os.RemoveAll(dbDir); err != nil {
```

## Workflows

Every action is referenced by the tag `.github/workflows/actions.lock` keys it on, and the lock
records the commit the runner executes. The tag is not the pin; the lock is, and a run whose
`uses:` the lock does not account for is refused at startup with no reason given. Never hand-edit
a ref or the lock — `gh actions-lock` owns both, and `make pins` fails when they disagree.
`make selfcheck` runs our own analysis over our own workflows and fails on any finding.

## Submitting a pull request

1. [Fork][fork] and clone the repository.
1. Create a branch: `git checkout -b my-branch-name`.
1. Write the test, watch it fail, then make your change.
1. Run `make verify`, and `make selfcheck` if you changed a workflow.
1. Push to your fork and [submit a pull request][pr] against `main`, saying what changed and why.
   Link an issue if one exists.

Keep unrelated changes in separate pull requests, and write a
[good commit message](https://cbea.ms/git-commit/).

## Resources

- [How to Contribute to Open Source](https://opensource.guide/how-to-contribute/)
- [About pull requests](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/about-pull-requests)
- [GitHub Docs](https://docs.github.com)
