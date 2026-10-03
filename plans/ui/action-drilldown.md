# An action's versions, one click from the inventory

**Status:** the Actions unit opens a row in place onto a page of the action's versions, and the details page
answers who `uses:` it, by reference and commit, from the shared walk under the address's scope;
its findings and its own dependencies are still read across every version, and every answer
reloads the whole graph to walk one node.

## Why this exists

A reader starts from Inventory › Actions, sees `actions/checkout` reached by 19 repositories through
53 distinct references, and wants to know which versions those are, what each one carries, and who
moves if one of them changes. The drill-down answers that in two steps:

1. The row opens in place onto the versions in use.
2. A reference opens the details page narrowed to it with `at`.

Two sections of the details page still answer for the action rather than for the version a reader
narrowed to, and the page pays for a whole-graph load on every request.

## Pending

1. **Findings by rule, narrowed to the versions asked for.**
   - The section counts findings in the action's own code at every analyzed commit, and says so.
     The versions table already reads findings per commit (`store.FindingsAtCommits`), so the
     section can read the same answer for the commits `at` selects.
   - Under `at=v4` it should say what `v4`'s commit carries, and nothing borrowed from `v3`'s.
2. **What the action uses, read at the version asked for.**
   - *Uses N dependencies* reads edge rows, which hold the last analyzed commit's declarations, and
     says so.
   - With `at` it should read the sightings at that reference's commit, as the walks do
     (`store.EdgeDeclarations`).
   - This is the details page's half of the one-version views decision in
     [`db/commit-aware-graph.md`](../db/commit-aware-graph.md).
3. **One walk without reloading the graph.**
   - `UsesOf` and `VersionsOf` each call `Builder.load()`. Measured on a copy of the crawled
     organization (18,744 nodes): the uses table answers in 0.27–0.35 s and the versions table in
     0.43–0.60 s, mostly load.
   - That is acceptable here and will not be at the enterprise target. Keep the model per graph
     generation and drop it when the generation moves; incremental derivation in the commit-aware
     plan is where it is maintained rather than rebuilt.
4. **What rules reported on the lines naming a version.**
   - No column carries it. The one that did named a single rule, `actions/vulnerable-action`, and
     reached its findings through the step they are attributed to.
   - Steps are tracked across commits and lines move between them. On the crawled organization,
     **1,650 of 2,423** `actions/unpinned-tag` findings reached through the step name another action
     or reference, and so do **433 of 1,155** matched by file and line.
   - A count by severity, from every rule, has to match a finding to the line at the commit its run
     analyzed: the edge's sighting at that commit, not the edge row.

## The constraints that must not be broken

- **A verdict comes only from a rule id.** Advisories travel verbatim beside the references, with
  their ranges, and never become a count of affected versions. No column is one rule's.
- **Direct and transitive are different work.** They are never added together, in *If this tag
  changes* least of all, and a lock that keeps a repository where it is says so separately.
- **Upstream repositories are not dependents.** The uses table hides them by default and counts
  them. The versions table lists only versions starting points use, has no column counting anybody
  else, and says how many versions it left out.
- **Not analyzed is not zero.** A version with no findings run at its commit reads *not analyzed*,
  and a reference nothing resolved reads *not resolved*.
- **A term is applied or refused by name, never dropped.**
  - The page reads its scope through `ScopeService` and sends each endpoint only the terms its
    surface applies (`ScopeService.PredicateFor`).
  - Terms that chose which actions an inventory listed stay in the address, named as not applied.
- **Every reach on the page is the shared walk.**
  `graph.TestTheUsesTableIsTheStoredReach` holds the table to `abom_node_reach`,
  `abom_node_version_reach` and the stored count of references in use, across six fixtures.
- **A row body always has its control.** A `MudDataGrid` given `ChildRowContent` and no hierarchy
  column draws the body under every row. That is how the Inventory panel briefly listed 133
  repositories under `v4`. Where rows do not open, the column is hidden, not absent.
- **Severity has its own column.** Folded into a name column, as the Query Catalog does, it cannot
  be sorted and the name is harder to read.

## Verification

- With `at=v4`, *Findings by rule* agrees with the versions table's `v4` row, and a finding sighted
  only at `v3`'s commit is absent.
- The cost of `UsesOf` and `VersionsOf` is measured either side of keeping the model, on a copy of
  the crawled organization, and the uses table still equals stored reach for every action.
- `e2e/specs/drilldown.spec.mjs` passes against a corpus copy, not only the empty database it skips
  on.
