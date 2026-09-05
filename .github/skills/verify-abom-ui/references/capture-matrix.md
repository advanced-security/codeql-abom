# Capture matrix

What to screenshot, and what each capture is for. Routes are indicative — discover the real ones
from the navigation with `read_page` rather than assuming this list is current.

Capture at 1440×900 unless a check says otherwise.

| # | Surface | Interaction before capture | Exists to catch |
| --- | --- | --- | --- |
| 1 | Landing (remediation) | none | Whether the first screen answers "what do I fix first"; coverage strip present |
| 2 | Landing, top row expanded | expand highest-priority row | Ranges, observed refs and direct/transitive split shown together |
| 3 | Remediation, unfixable advisory | find one with no published fix | That "no fix available" is distinguishable from "fixed in X" |
| 4 | Explorer — Topology, repository granularity | focus `actions/checkout`, hops 1 | Hub occlusion, label collision at ~200 nodes |
| 5 | Explorer — Topology, workflow granularity | same focus | Hairball threshold; whether truncation is disclosed |
| 6 | Explorer — Risk force | same scope, switch force | Whether urgency reads from position |
| 7 | Explorer — same scope, after switching back | switch force again | View-switch continuity and node trackability |
| 8 | Explorer — reloaded | reload the identical scope | Layout determinism |
| 9 | Explorer — transitive exposure path | focus `pypa/gh-action-pypi-publish`, hops 3 | Whether a multi-repository route reads as a chain |
| 10 | Explorer — 3D | toggle 3D on a dense scope | Whether 3D resolves overlap or worsens it |
| 11 | Actions inventory | none | Density, sort affordances, search discoverability |
| 12 | Repository detail | open a repo with many findings | Findings grouped by rule; rule IDs visible |
| 13 | Discovery, nothing started | fresh database, discovery never started | "Not started" distinguishable from "paused"; what pressing Start will cost is stated before it is spent |
| 14 | Discovery, each queue state | click each state filter in turn | Every empty result explains itself; counts on the filters make "none" unambiguous |
| 15 | Discovery, live | start discovery, watch two cycles | Whether motion communicates progress; whether Pause visibly takes effect before the cycle ends |
| 16 | Explorer — Coverage view | switch to Coverage on the whole graph | Legend swaps with the encoding; nodes that were never dependencies are not drawn as unknown |
| 17 | Explorer — following a run | enable Follow while discovery runs | New nodes arrive near their neighbors; the layout does not jump and the camera does not move |
| 18 | Follow control, nothing running | discovery stopped | Checkbox disabled and empty, with a reason; the interval stays settable |
| 19 | Empty state | point at a database with no advisories | That "nothing found" is distinct from "not yet analyzed" |
| 20 | Server unreachable | stop the server with a page open and following | The page says it lost the server rather than freezing on stale data |
| 21 | Narrow viewport | 900×700 | Reflow, truncation, whether the graph becomes unusable |

## Notes

**Use a populated database.** Every crowding, truncation and overlap failure needs real density
to appear. `/tmp/abom-real/abom.db` (204 repos, 23.5k nodes, 4.5k findings) is the reference
corpus.

**Capture 6, 7 and 8 are a set.** They only mean something compared with each other: continuity
across a force switch, and determinism across a reload.

**Capture 15 matters more than it looks.** The distinction between "we looked and found nothing"
and "we have not looked here" is one ABOM takes seriously everywhere else, and an empty state is
where an interface usually loses it.

**Captures 13, 14 and 18 are the pre-start set.** Most of a discovery interface's life is spent
with a full queue and nothing analyzed, and that is the state least likely to have been designed.
A bare grid with a pager reading `0-0 of 0` is the failure to look for.

**Capture 20 needs the server actually stopped**, not a throttled network: `make stop` while the
page is open. A view that follows a run has to say when it can no longer reach the server, or it
reports stale numbers indefinitely with no sign anything is wrong.
