# A scope you cannot build, name or keep

**Status:** the filter language can be written into an address bar by one screen, stored by an API
nobody calls from the interface, and composed by nobody. An operator who wants *the workflows my
team owns* has to type a query string.

## Why this exists

Policies, reports and remediation campaigns are each tied to a business unit, an application or a
vulnerability. All three start by naming a set of repositories and the things inside them — which is
what `properties=` becomes able to say, and what no screen offers a way to build.

`abom_saved_view` and its three routes exist and have no interface at all. The Explorer writes a
predicate to the address bar and no other page reads one. So the durable artifact an operator needs
— *this is my scope, here is its name, load it again next month* — can be produced by hand-editing a
URL and in no other way.

## Pending

1. **A scope control on the shared filter bar**: choose a key from the ones the organization
   administers, choose values from that key's breakdown, include or exclude, add a second key.
   `/api/properties` already returns both the keys and their counts, `properties=key:value` is
   already the term every endpoint that filters understands, and `.abom-filters` is already the row
   it goes in. Depends on nothing.
2. **Saved scopes.** List, name, load, delete, over the routes that already exist. A policy is a
   saved scope somebody took responsibility for, so this is also the screen the policy work lands
   beside rather than a page of its own. Depends on 1.
3. **The Explorer groups and filters at once** — owned resources grouped by the chosen key, drawn
   with their direct and transitive dependencies. Depends on 1, and on
   `plans/ui/explorer-legibility.md`: the collapsed view currently folds 2,639 nodes into 8 groups
   and draws two points, so grouping is correct and invisible.
4. **Coverage beside every filtered number.** Depends on `plans/api/filter-enforcement.md`.

## The constraints that must not be broken

**The interface names the property, never what it might mean.** ABOM is told a key. An organization
running `business_id` gets business units and one running `team` gets teams; which it is, is not
something ABOM knows. A screen says `business_id`, and the word for what that key means stays in the
vision document.

**A scope survives a reload, or it is a setting.** The canonical predicate is the address bar, so a
scope is shareable as a link by construction and a screenshot of one is reproducible.

**Showing a term is not enforcing it.** The control may only offer what its endpoint enforces, read
from the declaration rather than from a second list maintained here — which is the C# copy of
`ViewSpec` drifting from the server, in a new place.

**A scope that excludes most of the organization must say so.** The count of owned repositories
carrying no value for the chosen key belongs beside the filtered total, not behind a tooltip.

## Verification

- A scope built by clicking produces the same predicate text as the equivalent hand-written query
  string, and loads back into the same control state.
- The same scope applied on Discovery, the Bill of Materials and the Explorer selects the same
  repositories, checked against `/api` rather than against another screen.
- A saved scope reloaded a second time selects what it selected the first time.
- The seven screens are driven with a scope applied and their rendered numbers checked against the
  endpoints, in both color schemes. Every interface defect found in recent memory was found this way
  and none by a green build.
