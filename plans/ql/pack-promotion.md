# A query leaves ABOM as a published pack, never as an alert

**Status:** The ABOM-controlled deploy level is built — advancing a version to `deploy` rewrites a
local suite that tier bindings can run. Nothing is published anywhere, which is the level this
plan is about.

## Why this exists

There are two deploy levels and they answer to different authorities.

**Inside ABOM**, deploying means a query joins a suite the crawl can be bound to. That exists:
`abom/custom-queries:codeql-suites/abom-deployed.qls` is rewritten from the query ids at the
`deploy` stage on every advance, so withdrawing one removes it, and it is offered in the tier
binding grid beside the shipped suites. The development suite beside it is deliberately **not**
offered — it selects the whole pack, so binding it would make saving a file a crawl of the corpus.
Nothing leaves the machine at this level.

**Outside ABOM** is what remains, and it is somebody else's estate. ABOM holds findings that
terminate on its own screen. The obvious way out was to write code scanning alerts directly, and
that route is **refused**: an alert appears in somebody else's repository, in the Security tab
their developers read and in the numbers their leadership is measured on. A tool that can create
those is a tool that will eventually create them by accident.

There is a better route, and it is one GitHub already supports. A CodeQL configuration file applied
through the `github-codeql-config-file` repository property is **merged** with the configuration
Default Setup generates, so an organization can add query packs without switching to advanced setup
and without anybody editing a workflow. The property takes a value like
`remote=octo-org/config@main:codeql.yml`, and an organization-wide **default** value for that
property applies it to every repository that has not set its own.

That changes what ABOM is for at the end of the chain. **ABOM produces the configuration; GitHub
produces the alerts.** The organization publishes a pack it wrote and tested, points the property at
a config file that names it, and Default Setup picks it up on the next scan. Every alert exists
because somebody published a pack, which is a deliberate, reviewable, revertible act — and ABOM
never touches an alert at all.

This is also why the operator's queries live in a real pack on disk from the beginning
([`ql/query-authoring.md`](query-authoring.md)) rather than as rows in `abom.db`: a pack is the
unit that can be tested, versioned and published without ABOM in the loop.

## The constraint the built half already holds

**A deployed suite is a projection of the deploy stage, never a log of it.** It is rewritten whole
on every advance, so a query somebody took back stops running. A file that only ever grew would
keep a withdrawn query in the crawl with nothing on screen saying so.

**Nothing deployed runs nothing, not everything.** An empty set writes a suite that excludes every
id rather than one with no selection — a suite that selects nothing runs the whole pack, which is
the opposite of what an empty deploy set means, across the whole corpus.

## Pending

1. **Publish ABOM's own packs first.** The query packs under `ql/` — the actions library, graph
   extraction, findings and selfcheck, and four quality packs — are versioned at the CLI's
   version, checked by `make check-versions` inside `verify`, and consumed by nothing outside this
   repository. Publishing them to the GitHub Container registry under `advanced-security` is the
   smaller half of this plan and the one that proves the route. **The library goes first**: graph
   extraction and findings depend on it as `${workspace}`, which publishing turns into its exact
   version, so neither resolves from the registry until the library is there. Whether the quality
   packs, which analyze ABOM's own code, are worth publishing is part of the item. Depends on
   nothing.
2. **Publish an operator's pack.** Package and publish from the operator's own directory, to a
   registry the organization's repositories can read. **Packs must be accessible to the repositories
   that run code scanning**, which for a private organization means registry access is part of the
   work rather than a footnote. Depends on 1.
3. **Produce the configuration file, and hand it over.** ABOM knows which packs it is proposing and
   can write the `codeql.yml` that names them. It does not commit that file, does not create the
   repository property and does not set the organization-wide default. Depends on 2.
4. **Keep the catalog entry across the boundary.** A pack that has been published is still the same
   query ABOM has results for. The catalog records that it was promoted, to what version, and where
   — so the findings ABOM holds and the alerts GitHub raises are traceable to one another without
   either becoming the other. Depends on 3 and on [`db/query-catalog.md`](../db/query-catalog.md).
5. **Notice when the published version and the running version diverge.** A pack published at v3
   while ABOM keeps simulating v4 produces predictions about a query nobody is running. Depends
   on 4.

## What this replaces

**The code scanning upload route is refused, and this is what refuses it.** The earlier plan
required re-anchoring every transitive finding to a `uses:` line, a stable fingerprint that survived
re-anchoring, a commit on every finding, a tool name and category pair whose mishandling *deletes
somebody's alerts*, and a review-and-confirm gate to stand in front of all of it. None of that is
needed to get a query in front of a developer. Publishing a pack achieves the same outcome with the
organization's own consent mechanism, and ABOM writing nothing is a stronger guarantee than ABOM
writing carefully.

The consequence is that **ABOM's findings stay in ABOM**, permanently and by design. They are for
deciding what to publish, not for living with afterwards.

## The constraints that must not be broken

**ABOM never sets the property, never commits the config file, and never publishes without being
asked.** These are the writes that reach other people's repositories, and an automated path to any
of them is the thing this plan exists to avoid. A schedule, sweep or retry that publishes is a
defect, in the same class as one that uploaded.

**Publication is a person's act, and it is reversible by them.** Removing the pack from the config
file stops the alerts at the next scan. That property is worth stating because it is the reason this
route is safe and the upload route was not: an upload replaces, and a wrong category deletes alerts
that cannot be recovered.

**No alert exists that a person did not choose**, carried unchanged. It is now satisfied by
construction rather than by a gate: the choice is the publication.

**A pack is published at a version, never at a branch.** A consumer pins what it resolves, and a
moving target makes the prediction in
[`api/query-simulation.md`](../api/query-simulation.md) describe something other than what runs.

**A published query does not become an ABOM verdict.** Alerts it raises belong to GitHub's code
scanning and to the repository they appear in. ABOM keeps its own findings, and the two are
reconciled by naming the query rather than by merging the results.

**ABOM is not a second code scanning interface.** It is where a detection is authored, measured and
decided on. Once published, it is lived with in the place developers already look.

## Verification

- ABOM's four packs publish, resolve and install from the registry into a clean environment, and a
  suite resolved from a published pack returns the same query set as the local one.
- An operator's pack publishes from their own directory with ABOM not involved in the build, because
  that independence is the point.
- On `codeql-abom-test1`: publish a pack, point the property at a config file naming it, and confirm
  the alerts that appear match the prediction written down beforehand. **The disagreement is the
  finding**, and it is recorded rather than explained away.
- Reversal is checked at the same time: removing the pack from the config file stops the alerts, and
  the repository's other analyses are unchanged throughout.
- A test fails if any code path publishes, sets a property, or commits a configuration file without
  a recorded human action.
