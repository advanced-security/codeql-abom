---
description: "Use when writing, editing or debugging CodeQL queries, libraries or query tests in the codeql-abom ql/ pack. Covers the abom/ id prefix, why concat is total, recovering YAML pin comments, LSP false negatives on internal imports, and query test workflow."
applyTo: "ql/**"
---

# CodeQL queries in codeql-abom

## Trust the upstream queries

ABOM does not reimplement security analysis. `codeql/actions-queries` decides whether a use is a
problem; ABOM's own queries extract *structure* — what references what, and where.

Queries here therefore carry an `abom/` id prefix, which keeps them separable from upstream rule
ids everywhere downstream:

```ql
 * @id abom/edges
```

A structural query has no `@kind`, so it yields BQRS and no SARIF. That is deliberate — the
hosted MRVA backend will not run it, which is why the graph is built by local evaluation.

## `concat` is total

`concat(...)` returns `""` for an empty set rather than no result, so `exists(concatHelper(x))`
is **always true**. This silently mislabelled every job's permissions scope once.

Guard on the underlying set instead:

```ql
// wrong: always holds
exists(jobPermissions(job))
// right
exists(job.getPermissions().getAPermission())
```

## Pin comments are not part of the scalar

A trailing YAML comment such as `# v4.2.2` is not in the `uses:` value. Recover it through
`codeql.actions.ast.internal.Ast::Comment`, matched by same line and `startColumn` beyond the
uses node's `endColumn`. These are evidence and are stored verbatim — never normalised.

## The VS Code QL extension lies about internal imports

`private import codeql.actions.ast.internal.*` reports "could not resolve module", and one
unresolved import cascades into "could not resolve type" for every type in the file. The
extension is wrong. Confirm with the compiler before believing it:

```bash
codeql query compile ql/actions/abom/src/AbomEdges.ql
```

## Query tests

```bash
codeql pack install ql/actions/abom/test   # from the workspace root, so ${workspace} resolves
codeql test run ql/actions/abom/test
```

A test with no `.expected` file reports as failing. Read the `.actual` and review it **before**
`codeql test accept` — accepting blind bakes in whatever the query currently does, including its
bugs.

The test source trees under `ql/actions/abom/test/` double as fixture sources for Go tests.
Changing one means regenerating the fixtures that were produced from it.

## BQRS format notes

`bqrs info --format=json` uses short kind codes (`s`, `i`, `e`); `bqrs decode --format=json` uses
long names (`String`, `Integer`, `Entity`). They do not match, and code that assumes they do
fails only on the paths it was not tested against.
