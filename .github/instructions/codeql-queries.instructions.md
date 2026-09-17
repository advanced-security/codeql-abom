---
description: "Use when writing, editing or debugging CodeQL queries or libraries in the codeql-abom ql/ pack. Covers the abom/ id prefix, why concat is total, pin comments outside the scalar, LSP false negatives on internal imports, and BQRS kind-code differences."
applyTo: "ql/**"
---

# CodeQL queries in codeql-abom

**Fixed file.** Do not edit or extend it. Work goes in `plans/**`, what the system does in
`docs/*.md`, a lasting lesson in `.github/skills/**`. A rule here that is wrong: say so and stop.

## Extract structure, and author findings under `abom/`

ABOM writes its own queries: extraction queries that say what references what, and findings
queries that augment the upstream set. Both carry an `abom/` id prefix, which is what keeps them
separable from upstream rule ids downstream and lets a consumer filter on either.

`codeql/actions-queries` is the upstream security set. The default suite happens to select it
unchanged today; that is where things stand rather than a rule, and an ABOM-authored query belongs
there as soon as it earns a place in the default.

A structural query has no `@kind`, so it yields BQRS and no SARIF. Deliberate: the hosted MRVA
backend will not run it, which is why the graph is built by local evaluation. A findings query
needs `@kind` and, for `problem`, `@problem.severity` to reach SARIF at all.

## An Actions file with no `jobs:` is not a `Workflow`

The extractor creates no `Workflow` entity for a file under `.github/workflows/` that declares no
jobs — PrintAST shows `OnImpl` and `EventImpl` and no `WorkflowImpl`. So
`exists(Workflow w | not exists(Job j | j.getWorkflow() = w))` is unreachable rather than strict.
Per-workflow coverage comes from `Job` alone, which is sound because a valid workflow declares at
least one. `CompositeAction` is the case a job-only query misses: a repository that *is* an action
has no jobs at all.

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
