# Contributing

Thanks for taking the time to contribute.

## Before you start

```bash
make verify
```

That is the same command CI runs, so if it passes locally it should pass on the pull request. It
needs Go (version in `go.mod`), .NET 10, Node, the CodeQL CLI at the version in
`.codeql-version`, and `golangci-lint`.

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
numbers afterwards.

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

## Submitting

Open a pull request against `main` with a description of what changed and why. Link an issue if
one exists. Keep unrelated changes in separate pull requests.

By contributing, you agree that your contributions will be licensed under the MIT license in
[LICENSE](LICENSE).
