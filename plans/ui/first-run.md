# The first run draws itself

**Status:** planned. Found by the interface audit on 2026-09-07 and shaped by the decisions taken
after it. Pairs with `ui/owned-scope-editor.md`, which builds the declaration this begins from.

## Why this exists

Once `ui/closure-first-explorer.md` lands, the Explorer shows the owned closure and nothing else.
That is right, and on its own it makes the first run **two empty screens in a row**: a Settings
page where you declare an account, and an Explorer that stays blank until a full analysis cycle
has cloned repositories, built CodeQL databases and ingested edges — roughly fifteen seconds per
repository, serially visible as nothing at all.

Nothing on either page says what to press next.

That is a waste of the most interesting thing ABOM does. **Enumerating an account's repositories
is an API call, not an analysis.** It costs seconds, needs no CodeQL, and produces exactly the
frame the Explorer needs to stop being empty: the owner, and the repositories under it. Everything
after that — workflows, actions, the dependency edges between them — arrives incrementally through
machinery that is **already built and already running**.

So the first run should be: declare an account, watch its repositories appear, press Start, and
watch the supply chain draw itself outward from them.

## What is already true

| | |
| --- | --- |
| `ghauth.Verify` | already returns the accounts the operator belongs to, each with a `Kind` and a `Role` |
| `catalog.Source.ListRepositories(org)` | the enumeration. One API pass over an account |
| `catalog.Source.FindDatabase(repo, lang)` | a *different* question — does GitHub already host a prebuilt database. One call per repository |
| `discover.Run` | does both in one pass, so the cheap half is only reachable behind the expensive one |
| `discover` from the server | **absent** — not referenced anywhere in `internal/serve` or `internal/supervisor` |
| `runner.CreateDatabase` | ABOM builds its own actions databases, from a sparse checkout of `/.github/`, `action.yml`, `action.yaml` |
| `abom_database_catalog` | `repository`, `language`, `commit_oid`, `has_database`, `checked_at` |
| its only consumer | `CatalogRepositories`, `WHERE has_database = 1`, feeding `codeql-abom build` |
| rows in it on the reference corpus | **0** — that corpus was seeded from `gh mrva`, never discovered |
| repository nodes | already first-class: **1,098** of them, `node_kind='repo'` |
| repository nodes with outgoing edges | **0** — they are islands by construction |
| `repo-contains`, `org-contains` | synthesized at read time, so containment already draws |
| owner nodes | synthesized by `attachOwners`, not stored |
| drawing unmeasured nodes honestly | `discoveryColor`, and a test that a node never queued is not colored as a discovery state |
| live arrival | `FollowGraph`, `GetGraphDeltaAsync`, `mergeInto`, `placeArrivals` |

The shape is almost all there. Repository nodes exist, containment is drawn without being stored,
owner nodes are synthesized, arrivals are placed beside their neighbors, and the interface already
knows how to draw something nobody has measured without implying a measurement. **What is missing
is the step that turns a declaration into repository rows** — and even that is written. It is
welded to a question nobody asked, and reachable only from a terminal.

## The design

### Enumeration is a separate, cheap phase and the interface should say so

Discovery today is one button that means "clone and analyze". It is really two phases with costs
three orders of magnitude apart:

- **Enumerate** — ask GitHub what repositories the declared accounts hold. One API pass. Seconds.
  No CodeQL, no clone, no disk.
- **Analyze** — fetch, build, query, ingest. About fifteen seconds per repository.

The first runs on declaring an account, without being asked, because it is cheap and because its
result is the frame everything else appears in. The second stays behind Start, because it is not.

### Owning a repository and having a prebuilt database are unrelated facts

`discover.Run` answers them in one breath: it enumerates the account, then asks GitHub for a
prebuilt database once per repository. Only the first answers "what does this account hold". The
second answers "has somebody already turned on code scanning here" — a fact about how the
repository is configured, not about who owns it — and it costs an API call per repository, which
is what turns a seconds-long pass into a long one. **Split `Source` so the enumeration can run
alone.**

A prebuilt database, where one happens to exist, is a shortcut worth taking later:
`runner.CreateDatabase` need not build what could be downloaded. That makes `has_database`
**provenance, not eligibility**. Nothing may gate analysis on it.

The conflation is not a design; it is the seam where this corpus came from. It was seeded through
`gh mrva`, which could only ever see repositories that already had databases, and the catalog
still carries that shape.

### Membership is what fires it, not a button

`ghauth.Verify` already establishes which accounts the operator belongs to and with what role, and
the scope editor already marks a declared account it cannot confirm. That check is the gate: **an
account confirmed by `gh` enumerates immediately**. Once GitHub has said the operator belongs
there, a second button asks them to confirm something already confirmed.

An account `gh` does not report stays declarable — `ui/owned-scope-editor.md` keeps that, for
accounts a token cannot see — but it does not enumerate on its own. That is what stops a typo
sending a repository listing after a stranger's account, and it makes the confirmed mark on the
Settings page load-bearing rather than decorative.

Failing to reach `gh` is not the same as belonging to nothing. `ghauth.Problem` already carries
that distinction along with the command that fixes it, and enumeration must not read a broken tool
as an empty account.

### A repository has three states and they must not look alike

- **Enumerated, not analyzed.** We know it exists and have not looked inside. Drawn, and drawn as
  unexamined — never at the smallest size in a view where size means reach, which would assert
  that nothing depends on it.
- **Analyzed.** Its workflows and their references are in the graph.
- **Nothing to analyze.** It could not be read, or holds no `.github/` directory and no action
  manifest. `expand.ErrUnavailable` is already precisely this, and it is a real coverage
  statement: there is no supply chain here to draw.

A reader must be able to tell "we have not looked" from "there was nothing to look at", because
only one of them is work somebody can do.

**"No CodeQL database" is not one of these states.** ABOM builds its own from a sparse checkout, so
a repository with code scanning switched off is analyzed exactly like one with it on. The
frontier's `unavailable` is described in `objective.go` as what becomes of "a dependency GitHub has
no database for" — which names the catalog's question rather than the one `expand` actually
answers. It should say what `checkout.go` says: unreadable, or holding nothing to build from.

### An owned repository nobody could examine is still on the bill

The bill of materials counts references, so a repository enumerated and never analyzed contributes
nothing and is simply absent — which reads as clean when it means unexamined. Owned repositories
belong on the bill in their own right, each carrying which of the three states it is in.
`api/bill-of-materials.md` needs to make that coverage statement; this plan supplies the rows it
would make it from.

### The empty state is a stage, not an apology

Before anything is declared, the Explorer says so and points at Settings. After a declaration and
before enumeration finishes, it says what it is waiting for. After enumeration it is already a
picture — accounts and their repositories — and Start is the obvious next thing.

## Phases

1. Split `catalog.Source` so enumeration runs without the per-repository code-scanning probe.
2. Enumeration reachable from the server, over the declared accounts rather than a `--org` flag,
   fired by the membership check rather than by a button.
3. Enumerated repositories become nodes, in the three states above.
4. The Explorer's empty and waiting states, and the frame that follows them.
5. The scope editor says what to press next, and the Discovery page distinguishes enumeration from
   analysis in its controls and its counts.

## Verification

- From an empty database: declare an account, and repository nodes appear **without any CodeQL
  invocation and without a button being pressed** — checked by watching that no database is built.
- An account `gh` reports no membership of is not enumerated, and the page says why. A `gh` that
  cannot be reached does not produce that same answer.
- Enumeration issues one API pass, not one call per repository — asserted against the fake
  `Source`, which is what makes it a test rather than a stopwatch.
- A repository with nothing to analyze is visibly distinct from one not yet analyzed, and a reader
  can say which is which from the screen alone.
- An enumerated repository is not drawn at the size that means "nothing depends on this".
- A repository never probed for a prebuilt database does not read as one shown not to have one.
- Analysis reaches a repository whose `has_database` is false.
- Pressing Start then produces visible arrivals against that frame — the check
  `ui/closure-first-explorer.md` also makes, and the reason both plans exist.
- Enumeration of a large account does not block the interface, and its cost is recorded in the
  cycle accounting `db/freshness-budget.md` added.

## Open questions

- **Where an enumerated repository is recorded.** `abom_database_catalog.has_database` is `NOT
  NULL`, so enumerating without probing has to write `0` — asserting "code scanning is off here"
  about a repository nobody asked about, which is absent-is-not-zero broken in the schema itself.
  Either the column becomes nullable, with `NULL` meaning *not probed*, or ownership gets its own
  table and the catalog goes back to being only about prebuilt databases. The second is truer to
  what the two things are.
- **Whether enumeration should follow role as well as membership.** `ghauth` records `admin` or
  `member`, and a member of a large organization can enumerate far more than they answer for.
  Nothing in ABOM reads `Role` yet.
- **What becomes of a repository that disappears.** Enumeration is a snapshot, and a repository
  present last week and absent today is a fact worth keeping rather than a row to delete.
