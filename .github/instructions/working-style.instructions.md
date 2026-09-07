---
description: "Use when writing or changing code in codeql-abom, and before claiming work is done. Covers test-first discipline, validating against the real corpus with a before/after diff, reading zero results as a coverage gap, refusing to 'fix' data that contradicts an assumption, the comment rule, the make targets that define verified, and why nothing that shells out to CodeQL may run while discovery is crawling."
applyTo: "{cmd,internal,ql,ui}/**"
---

# Working style and verification

## TDD, strictly

Write the test, run it, watch it fail for the expected reason, then implement. A test that has
never failed is theatre: one asserted a string the component never renders and was green against
deliberately broken code. Tests are named after the behavior they protect, not the function they
call.

## Validate against real data, with a before/after diff

Unit tests passed through every architectural bug this repository has had. Snapshot the real
database before a pipeline change and diff the headline metrics after:

```bash
cp abom.db abom-before.db   # then compare blast radius, node/edge counts, findings per rule
```

Never point work at the live database when a copy will do.

## Zero results usually means a coverage gap, not a broken traversal

Before touching traversal code, check whether the data supports the result at all — e.g. compare
`blast_radius.dependent_repos` against direct-edge repo counts.

## Never "fix" data that contradicts an assumption

`actions/download-artifact@v8` looked like corruption; `git ls-remote --tags` confirmed v1–v8 all
exist. Verify the world first.

## Comments

State only what the code cannot show, in one line. Explain *why* a constraint exists, never what
the next statement does.

```go
// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
if err := os.RemoveAll(dbDir); err != nil {
```

## Verification

Run before declaring work complete:

```bash
make verify              # check-corrupt, check-versions, fmt, vet, lint, test, js, style, ql, ui, ui-test
make itest               # needs the CodeQL CLI; builds a database and exercises the real protocols
make selfcheck           # needs the CodeQL CLI; not in verify because it takes tens of seconds
```

`make verify` is the only definition of "verified", and CI runs the same target. Do not assemble
an equivalent by hand: `check-corrupt` runs first and is not optional, because this environment
intermittently prepends a duplicate `package` clause when writing a Go file, which `gofmt -l`
reports as a parse error rather than a diff.

`make itest` is what `verify` cannot be: the CodeQL query server is the CLI's IDE-facing
plumbing, so its message shapes are not a stable interface and a mismatch is invisible to every
unit test — it answers `Internal error` with a Java stack trace and says nothing about the shape
being wrong. Run it after touching `internal/runner` or anything that speaks to the CLI.

## Running the service

One canonical lifecycle, so nobody re-derives it from shell history:

```bash
make start               # build + publish + serve in the background on $(ABOM_ADDR)
make status              # is it up, and what is discovery doing
make logs                # follow it; one line per cycle and per failure
make restart / make stop
make serve               # foreground, for watching it work
```

Override `ABOM_DB`, `ABOM_ADDR`, `ABOM_UI`, `ABOM_WORK` rather than editing the Makefile.
Discovery never starts on its own: `make start ABOM_SERVE_FLAGS=--expand` opts in. Run one
server, not several; two against one database is two writers, which the store forbids.

**Never run `make verify`, `make ql` or `make selfcheck` while discovery is running**, and the
same goes for the CodeQL MCP server's tools. Every `codeql` invocation shares one local
compilation cache, so a query test and a dependency's database build in flight together contend
for that cache as well as for every core. Check with `make status` first, and pause discovery
from the Discovery page or `make stop`. This is also why a timing-sensitive failure during a
crawl is not evidence of anything until it reproduces on a quiet machine.
