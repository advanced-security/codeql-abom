---
name: stage-codeql-actions-callees
description: 'How codeql/actions-all resolves a `uses:` and how to build an actions database whose dataflow crosses into dependencies. Use when staging dependencies into a CodeQL actions database (closure or multi-repository databases), setting LGTM_INDEX_FILTERS or a code scanning config for actions extraction, deciding which dependencies need copying in, counting staging collisions, or writing a query test that depends on what the extractor indexes. Covers which callees resolve (composite actions and reusable workflows only), the staging layout, index filter defaults and how to extend them, and the traps that resolve one reference to the wrong file.'
---

# Stage callees into a CodeQL actions database

Verified against CodeQL CLI 2.27.1 and `codeql/actions-all` 0.6.2. Re-check after a pin moves:
`ql/actions/graph-extraction/test/StagedCallees` fails when resolution changes.

## How a `uses:` resolves

Every `uses:` is a `DataFlowCall`. `viableCallable` is string equality:

- **Call side:** the `uses:` value up to the first `@`. The ref is discarded.
- **Callee side:** the file's relative path with the repository root, `/action.yml` (or
  `.yaml`) and `.github/actions/external/` or `.github/workflows/external/` removed.

Only two things are callables:

| Callee | Condition | Resolves when staged |
| --- | --- | --- |
| Composite action | `action.yml` with `runs.using: composite` | yes |
| Reusable workflow | workflow with `on: workflow_call` | yes, once indexed (below) |
| JavaScript or Docker action | anything else | **never**, staged or not |

**Stage composite actions and reusable workflows; never stage anything else.** A JavaScript or
Docker action contributes nothing to dataflow. Known ones are modeled by name through the library's
models-as-data. Count collisions only over what is staged: counting every action overstates them
several-fold, because most version disagreements are between majors of JavaScript actions like
`actions/checkout`.

**Decide "composite" at the selected commit from the checked-out `action.yml`.** The ABOM graph
records a composite only when it has a `uses:` step, so one made only of `run:` steps leaves no
trace in it. That is the usual injection sink.

## Layout

| Callee | Staged at | `uses:` it matches |
| --- | --- | --- |
| Composite | `.github/actions/external/<owner>/<repo>[/<path>]/action.yml` | `<owner>/<repo>[/<path>]@…` |
| Reusable workflow | `.github/workflows/external/<owner>/<repo>/.github/workflows/<file>` | `<owner>/<repo>/.github/workflows/<file>@…` |

The namespace is flat: depth costs nothing, and a taint path crosses any number of staged
boundaries. One slot per `owner/repo[/path]` means **one selected commit per staged repository per
database**. Two selected commits of one staged callee is a collision to refuse, never a choice to
make by write order.

## Index filters

The actions extractor's `tools/autobuild.sh` defaults are:

```text
exclude:**/*
include:.github/workflows/*.yml          include:.github/workflows/*.yaml
include:.github/reusable_workflows/**/*.yml   include:.github/reusable_workflows/**/*.yaml
include:**/action.yml   include:**/action.yaml   include:**/actions.lock
```

- Workflows are indexed **only at the root**, so a staged reusable workflow is silently absent.
  Append `include:.github/workflows/external/**/*.yml` and `…/*.yaml` through `LGTM_INDEX_FILTERS`.
- `autobuild.sh` puts the provided value **after** the defaults, so appending keeps them.
- A code scanning config's `paths:` becomes `exclude:**/*` plus includes, which **replaces** the
  defaults and drops every `action.yml`. Use `LGTM_INDEX_FILTERS` or `paths-ignore`, never `paths`.
- `--codescanning-config` applies `paths-ignore` at extraction. `query-filters` apply only when the
  codeql-action selects queries, and an explicit `--suite` bypasses them.
- `codeql test run` extracts every `.yml`/`.yaml` by the extractor's `file_types`, ignoring these
  filters and `tools/baseline-config.json`. A query test cannot show what `database create` indexes.

## Traps

- **`.github/reusable_workflows/<nwo>/` is a default include with no library support.** The call
  stays unresolved, and the copy can still bind a root `./` reference to a file the repository does
  not contain. Refuse it.
- **More than one repository root resolves `./` across trees.** `getRepoRoot()` yields one root per
  workflow-bearing tree and is applied to every callable. With a staged tree at `<owner>/<repo>/`,
  or several owned repositories in one database, `./.github/actions/setup` binds to every tree's
  `setup`. Refuse bare sibling staging. Admit a multi-repository database only after measuring that
  no two trees share a local callable path.
- **No workflow anywhere, nothing resolves.** `getRepoRoot()` needs a `Workflow`. Anchor a tree
  that has none with a synthesized workflow, and exclude that file from ingestion by path.
- **A staged callee's own `./` references never resolve**, because it contributes no root. This is
  a coverage gap, not wrong answers.
- **A file with no `jobs:` is not a `Workflow`.** This is why a repository that is itself an action
  needs an anchor.
- **No overlays.** The actions extractor declares no `overlay_support_version`, so `--overlay-base`
  exits 0 and does nothing. `TestTheActionsExtractorStillCannotBuildAnOverlayBase` guards this.
- **Nothing reads a ref from a path.** Staged paths match `uses:` strings byte for byte once the ref
  is stripped. Never encode a ref in a directory name, and never rewrite `uses:` text in a copy.

## Check a layout before building on it

```bash
LGTM_INDEX_FILTERS='include:.github/workflows/external/**/*.yml' \
  codeql database create db --language=actions --source-root=tree --overwrite
codeql query run --database=db ql/actions/graph-extraction/test/StagedCallees/StagedCallees.ql
```

Every staged callee should name its file. `unresolved` means it was not indexed, or it is not a
callable. Run `codeql/actions-queries`' `CodeInjectionCritical.ql` over the same database: a taint
path into a staged `run:` confirms that dataflow crosses the boundary.
