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
sqlite3 abom.db ".backup abom-before.db"   # then compare blast radius, node/edge counts, findings per rule
```

**Not `cp`** — the database is in WAL mode, so a file copy silently drops everything committed
since the last checkpoint and you diff against a baseline that is quietly wrong.

Never point work at the live database when a copy will do.

## Zero results usually means a coverage gap, not a broken traversal

Before touching traversal code, check whether the data supports the result at all — e.g. compare
`blast_radius.dependent_repos` against direct-edge repo counts.

## Never "fix" data that contradicts an assumption

`actions/download-artifact@v8` looked like corruption; `git ls-remote --tags` confirmed v1–v8 all
exist. Verify the world first.

## A refactor proves it changed nothing, or it is not a refactor

Capture the endpoint responses before and after and compare them byte for byte with `cmp`.

## An unpublished project has no legacy

Nothing here ships to anyone yet, so a name introduced this week is replaced rather than deprecated:
no compatibility shim, no second spelling parsed "for now", no migration for data written before the
change. Calling week-old work legacy is how a wrong name becomes permanent, and every shim is a
second code path that has to be tested and will drift. Stored predicates and databases are rebuilt
from the crawl, not carried forward.

## An instrument that changes what it measures is worse than none

Its numbers look like evidence. No `-race` in a benchmark, no per-row timing, and every benchmark
states the corpus and the cache state it ran against. Rebuild before measuring — a served binary
from the previous build produces byte-identical "after" numbers, and they read as a null result
rather than as the mistake they are.

An instrument earns its place by contradicting the person who installed it. Prefer a proof to a
stopwatch: assert a query plan or a call count, not elapsed time.

## Zero from a tool that cannot see a defect reads like health

A category no check covers reports zero, and zero looks like a clean bill. Say which categories are
unguarded rather than quoting the total.

## A gate turns on last, and is never weakened to pass

Run a new check in report mode until the threshold is already met, then make it blocking. If a
threshold has to be raised to go green, the work is not finished — that is not evidence the
threshold was wrong.

## End-to-end beats argument

A cycle of defects that `make verify` passed: six predicates parsed and ignored, a scope reading the
wrong column, a total that double-counted, a frame rate off by an order of magnitude. Each was found
by driving the real thing against the real corpus. Where work can be checked end to end, check it
there before claiming it.

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
