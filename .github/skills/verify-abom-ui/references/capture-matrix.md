# Capture matrix

What to screenshot, and what each capture is for. Routes are indicative — discover the real ones
from the navigation with `read_page` rather than assuming this list is current.

Capture at 1440×900 unless a check says otherwise.

| # | Surface | Interaction before capture | Exists to catch |
| --- | --- | --- | --- |
| 1 | Dashboard | none | Whether the first screen hands over a useful scope; four nav entries and no more |
| 2 | Inventory — dependencies | none | Density, the *Showing N of M* line, tabs and units both legible at once |
| 3 | Inventory — sorted by findings | click Findings twice | The first row matches `?sort=findings&desc=true` from the API, not the page |
| 4 | Inventory — repositories unit | switch unit | Repository-shaped columns; no node column reading 0 for every row |
| 5 | Inventory — findings review | open a finding | Recorded query help, locations and code flows fit without horizontal overflow |
| 5 | Inventory — advisories unit | switch unit | Direct/transitive split; the line naming advisories that reach nothing |
| 6 | Inventory — a named view | click a tab | The tab reads as selected and the scope in the address matches its predicate |
| 7 | Inventory — narrowed by a property | narrow by a property value | The count of repositories carrying no value is stated beside the filtered total |
| 8 | Inventory — a refused scope | arrive with `?severity=high` | The refusal names the term rather than showing an empty grid |
| 9 | Explorer — neighborhood, repository granularity | focus `actions/checkout`, hops 1 | Hub occlusion, label collision at ~200 nodes |
| 10 | Explorer — workflow granularity | same focus | Hairball threshold; whether truncation is disclosed |
| 11 | Explorer — Priority force | same scope, switch force | Whether urgency reads from position |
| 12 | Explorer — same scope, after switching back | switch force again | View-switch continuity and node trackability |
| 13 | Explorer — reloaded | reload the identical scope | Layout determinism |
| 14 | Explorer — transitive exposure path | focus a dependency three hops deep | Whether a multi-repository route reads as a chain |
| 15 | Explorer — 3D | toggle 3D on a dense scope | Whether 3D resolves overlap or worsens it |
| 16 | Explorer — Coverage view | switch to Coverage on the whole graph | Legend swaps with the encoding; nodes that were never dependencies are not drawn as unknown |
| 17 | Explorer — following a run | enable Follow while discovery runs | New nodes arrive near their neighbors; the layout does not jump and the camera does not move |
| 18 | Discovery, nothing started | fresh database, discovery never started | "Not started" distinguishable from "paused"; what pressing Start will cost is stated before it is spent |
| 19 | Discovery, each queue state | click each state filter in turn | Every empty result explains itself; counts on the filters make "none" unambiguous |
| 20 | Discovery, live | start discovery, watch two cycles | Whether motion communicates progress; whether Pause visibly takes effect before the cycle ends |
| 21 | Follow control, nothing running | discovery stopped | Checkbox disabled and empty, with a reason; the interval stays settable |
| 22 | Empty state | point at a database with no advisories | That "nothing found" is distinct from "not yet analyzed" |
| 23 | Server unreachable | stop the server with a page open and following | The page says it lost the server rather than freezing on stale data |
| 24 | Narrow viewport | 900×700 | Reflow, truncation, whether the graph becomes unusable; whether tabs and units wrap readably |
| 25 | Settings — property scopes | declare an organization by property, then press Add property scope | Each scope is one line with its count; a held one says why and shows no count; a value as long as a config path wraps beside its remove button; naming the default is warned on the line and in the editor |

## Notes

**Use a populated database.** Every crowding, truncation and overlap failure needs real density
to appear. An empty database skips exactly the captures worth taking.

**Captures 2 to 8 are the Inventory set**, and they are where this interface's claims live now.
Four questions that used to be pages are units, tabs and actions here; each was folded in only
once its columns were its own, so the thing to look for is a column that belongs to a different
unit reading zero.

**Captures 11, 12 and 13 are a set.** They only mean something compared with each other:
continuity across a force switch, and determinism across a reload.

**Capture 22 matters more than it looks.** The distinction between "we looked and found nothing"
and "we have not looked here" is one ABOM takes seriously everywhere else, and an empty state is
where an interface usually loses it.

**Captures 18, 19 and 21 are the pre-start set.** Most of a discovery interface's life is spent
with a full queue and nothing analyzed, and that is the state least likely to have been designed.
A bare grid with a pager reading `0-0 of 0` is the failure to look for.

**Capture 23 needs the server actually stopped**, not a throttled network: `make stop` while the
page is open. A view that follows a run has to say when it can no longer reach the server, or it
reports stale numbers indefinitely with no sign anything is wrong.
