---
description: "Use when writing, editing or debugging CodeQL queries or libraries in the codeql-abom ql/ pack. Covers the abom/ id prefix, why concat is total, pin comments outside the scalar, LSP false negatives on internal imports, and BQRS kind-code differences."
applyTo: "ql/**"
---

# CodeQL queries in codeql-abom

## Extract structure, never verdicts

`codeql/actions-queries` decides whether a use is a problem; ABOM's queries extract what
references what, and where. Queries here carry an `abom/` id prefix so they stay separable from
upstream rule ids downstream.

A structural query has no `@kind`, so it yields BQRS and no SARIF. Deliberate: the hosted MRVA
backend will not run it, which is why the graph is built by local evaluation.

## `concat` is total

It returns `""` for an empty set, so `exists(concatHelper(x))` is **always true**. This silently
mislabelled every job's permissions scope once. Guard the underlying set:

```ql
exists(job.getPermissions().getAPermission())   // not exists(jobPermissions(job))
```

## Pin comments are not part of the scalar

A trailing `# v4.2.2` is not in the `uses:` value. Recover it via
`codeql.actions.ast.internal.Ast::Comment`, matched on same line with `startColumn` beyond the
uses node's `endColumn`. Evidence — store verbatim, never normalized.

## The QL extension lies about internal imports

`private import codeql.actions.ast.internal.*` reports "could not resolve module", and one
unresolved import cascades into every type in the file. Confirm with the compiler:

```bash
codeql query compile ql/actions/graph-extraction/src/AbomEdges.ql
```

## BQRS kind codes differ by command

`bqrs info --format=json` uses short codes (`s`, `i`, `e`); `bqrs decode --format=json` uses long
names (`String`, `Integer`, `Entity`).

## Exact pins are load-bearing, and live in three places

A floating upstream version means the same command on the same commit reports a different number
tomorrow, and a baseline that moves on its own is not a baseline. The pinned version is written in
the suite, the pack manifest and the lock file — and `codeql pack upgrade` rewrites two of them,
never the one inside the `.qls`.

A suite is identified as `<pack>:codeql-suites/<name>.qls` and is offered simply by being dropped
into a pack — no release, and no CLI call on the request path. Pack names are scoped before
publication, because renaming afterwards is not a rename.

CLI calls that take a pack reference need `--additional-packs ql`, since these packs are not
published.

## Tests

Test trees double as Go fixture sources — see the `regenerate-abom-fixtures` skill.
