# Take what code scanning already computed

**Status:** a download stands in for a build, and an owned repository's analysis answers for each
findings rule it ran, matched by rule id whatever CodeQL, pack or release ran it, with every rule
recording what produced it. Each cycle's log line says what code scanning supplied and why it
refused the rest; the crawl's on-and-off totals are not measured, and Settings does not show the
counts.

## Why this exists

Downloading what code scanning already produced is cheaper than producing it again. On 2026-10-05:

- **At least 20 of the first 40 seed repositories** held an actions CodeQL database on GitHub, 50 to
  220 KB each — public dependencies such as `actions/cache` and `actions/runner` among them. The
  database endpoints are Contents read, which the App holds, and a public repository's database is
  readable by any token.
- **Databases line up with ABOM's CodeQL; analyses rarely run ABOM's packs.**
  `advanced-security/codeql-abom-test1`'s database records `creationMetadata.cliVersion: 2.27.1`, the
  CodeQL ABOM runs. Default setup runs the bundle's `codeql/actions-queries`, which moves with every
  CodeQL release, while ABOM's lane is its own suite pinning a release and adding ABOM's rules. What
  the two share is rule ids: all 23 rules test-repo-3's analysis ran are in ABOM's lane.
- **A SARIF from code scanning names every rule that ran, not just the ones that fired.** test1's
  analysis lists 23 rules with 0 results; `rules_count` agrees. nacos ran the 17 of the default
  suite, with 13 results.
- Building a database is 13% of a repository's analysis and the two lanes are 87%
  (`expand.pendingWork`'s measurement). A download replaces the build; code scanning's results
  replace the part of the findings lane it ran — 23 of the rules `actions-all.qls` selects, where
  the repository runs `security-extended`.

## Decisions

1. **A downloaded database stands in for a build only when it is the database ABOM would build.**
   Three conditions, each read from the database itself:
   - the same commit, `creationMetadata.sha`;
   - the same CodeQL, `creationMetadata.cliVersion` — the rule the database cache already applies,
     because another release's extraction must not stand under this one's name;
   - the same files: its `src.zip`, relative to `sourceLocationPrefix`, holds exactly the files the
     checkout holds under the extractor's default includes (`.github/workflows/*.yml|yaml`,
     `.github/reusable_workflows/**/*.yml|yaml`, `**/action.yml|yaml`, `**/actions.lock`). A code
     scanning `paths:` replaces those defaults and `paths-ignore:` subtracts from them; either makes
     it a different database.

   Anything else and ABOM builds, counting why. A database that passes has the identity ABOM's own
   build would have had, so the ledger and the database cache need no new dimension.
2. **Code scanning's analysis answers for each findings rule it ran, matched by rule id.** By the
   operator's decision, a rule id names one query across packs and suites: a pack or suite importing
   another's queries does not change their ids, and code scanning is unlikely ever to run ABOM's
   packs exactly, so requiring them would leave nothing to take. The match is the repository, the
   commit and the rule id. The CodeQL, the pack, its release and the suite are not compared, and the
   analysis answers whether ABOM took the database or built its own. Rules are read from every tool
   component: the CLI lists them under its driver, GitHub's rendering under each pack.

   What the document cannot say, which files it read, is still established: default setup must have
   made the analysis, as its key `dynamic/github-code-scanning/codeql:analyze` says, because default
   setup reads the extractor's default files, where a repository's own workflow may narrow them with
   `paths` or `paths-ignore`. By the operator's decision its threat model is not compared: the only
   alternative to `remote`, `remote_and_local`, adds local sources — file paths and contents,
   command-line arguments — so it reports every result `remote` does and perhaps more, which ABOM
   would rather have than not. Default setup's configuration is therefore not read at all, and its
   first analysis is taken like any other.

   **A result is recorded under what produced it.** The lane stays one evaluation, the unit the
   ledger skips on, and each rule records its producer: ABOM's rules their query version and pack; a
   taken rule the analysis (`taken_from`), the pack and release its tool component names, the CodeQL
   its driver names (`cli_version`), and no query version, since none was read. Code scanning's
   document is ingested as its own, held to its own CodeQL rather than the ledger's, and the review
   panel says *Evaluated by: code scanning analysis N*.

   **A rule's results follow the release that ran it, and that is accepted with the decision.**
   Measured: has-ghas/build-applications-w-copilot-agent-mode's newest analysis of its commit ran
   CodeQL 2.23.1 with pack 0.6.9 and reported 13 of the 20 `actions/unpinned-tag` results ABOM's
   0.6.36 evaluation stores; the other 7 are reusable-workflow calls that release did not check.
   Taken, that commit carries the 13, under code scanning's analysis and release.

   GitHub serves its own rendering of the SARIF: rules sit in the pack's extension without
   `problem.severity` or `kind`, and results gain `level` and alert numbers. A taken rule's stored
   documentation is GitHub's descriptor, and a `recommendation` rule would store its `note`.
3. **Any repository for the database, owned repositories for the analysis.** Analyses are read with
   code scanning alerts read, which the App holds only where it is installed, and the operator scoped
   taking them to owned repositories. A public repository's database is readable everywhere.
4. **Asked at analysis time, through the shared response cache.** One conditional request answers
   whether code scanning holds an actions database; an unchanged answer costs no primary quota. A
   remembered answer would be stale exactly when it matters, after a push.

## Pending

1. **A push to an owned repository with default setup can wait for code scanning.** Today a push is
   analyzed within about 25 seconds, minutes before code scanning finishes, so reuse never applies
   to it. Holding the analysis until `workflow_run` reports `dynamic/github-code-scanning/codeql`
   completed at the pushed commit trades that latency for the build and most of the findings lane.
   It needs a queue state of its own — waiting on code scanning is not pending and not failed — and
   a ceiling after which ABOM builds anyway. `security_and_analysis` says when code scanning was
   switched on or off, which decides whether there is anything to wait for.
2. **Settings says what was taken and why not.** `expand.Stats.CodeScanning` carries each round's
   takes and its refusals as `database: <reason>` or `analysis: <reason>`, and the cycle's log line
   prints them; nothing persists them or serves them. A database is refused for no database,
   another commit, another CodeQL, other files or not readable, and is not asked for while
   `LGTM_INDEX_FILTERS` widens ABOM's own builds; an analysis for no analysis, another configuration
   (no analysis of the commit is default setup's), no rules in common or not readable. An unowned repository's analysis is never asked for, and is not
   a refusal.

## The constraint that must not be broken

**A result is recorded under what produced it.** A downloaded database stands in for a build only
when it is the database ABOM would build, because ABOM's own queries then run on it. A rule answered
by code scanning names the analysis, the release and the CodeQL that ran it, never ABOM's, and is
taken only from default setup, which establishes the files the document does not name. Whatever
cannot be established is evaluated by ABOM, never guessed.

## Verification

- A crawl of the same corpus with reuse on and off records identical node and edge totals, and
  identical findings for every taken rule whose release is ABOM's; a rule taken from another release
  differs only as that release's results do. `database create` invocations drop by exactly the
  downloads accepted. Asserted by counting invocations, never by elapsed time.
- test1, whose default setup reads `remote_and_local`, takes its database and the rules its analysis
  ran.
- A repository whose code scanning `paths-ignore:` drops a workflow is built, and the refusal says
  so.
- Whether an App installation token reads a public repository's database outside its installations
  is not observed; a user token reads `actions/cache`'s, and a 403 is refused as not readable.
