# A unit is a node kind, and its columns are its own

**Status:** five units, each asking its own endpoint for its own columns. The rule holds for what a
row says and not yet for what a reader can do with it, and one page still sits outside the
surface-and-unit taxonomy altogether.

## Why this exists

Splitting the listing by node kind settled what a row *says*. It did not settle what a reader can
*do* with a row, and those are the same question asked twice: an action offered against a row it
cannot apply to is the defect a structurally-zero column was, arriving through the toolbar instead
of through the grid.

## The shape

**One tab per node kind, or per family of kinds that share a column set.** Actions, Workflows, Jobs,
Repositories, Advisories and **Relationships** are built. Relationships were the sixth shape and the
last to arrive, because a unit only exists once its columns are its own and an edge had none: it
now carries its identity, what it crosses, how its reference is written, and what was reported
against the declaring line. See [`../../docs/architecture.md`](../../docs/architecture.md).

## Pending

1. **A unit's actions are its own, like its columns.** Four actions sit in the scope bar *above* the
   unit strip, so all five units offer all four of them: `bom`, `bom-cyclonedx`, `bom-spdx` and
   `open-in-explorer`. Two of them do not survive the change of unit.

   - *Show these in the graph* hands the scope to `/explore`, which draws nodes. An advisory is not
     a node and a repository is not a node, so on two of the five units the button either answers a
     different question than the one on screen or none at all.
   - A bill of materials is a document about components. Asked from the Jobs or Advisories unit it
     exports exactly what it exports from Actions, which is the definition of a control ignoring
     the unit it was pressed on.

   What is owed is a rule for which actions a unit offers, and a home for the ones that belong to
   the scope rather than to any unit. Note what is *not* wrong: no per-row action is offered here at
   all, and *re-analyze* lives in Settings, not on Inventory. Depends on nothing.
2. **The bill of materials is a document, not a listing, and is the one page that is neither a
   surface nor a unit.** It reads the shared scope and its export carries it, so it no longer
   invents one; what it still has is a route of its own and no place in the navigation. It states
   coverage, direct-versus-indirect depth and the advisory caveat that nothing on any surface says,
   so it cannot simply be deleted. Either those statements move onto Inventory and the page goes,
   or the page becomes an explicit fifth thing with a reason written down. Depends on nothing.

## The constraints that must not be broken

**A unit only exists once its columns are its own.** This plan exists because that rule was applied
to one kind rather than to the rule itself. Adding a sixth unit whose columns belong to a fifth is
the same mistake again.

**An absent measurement is not zero, and a structurally-inapplicable one is not zero either.** A
workflow's *Repos reached* is not a small number; it is not a number. A column that cannot apply to
a row does not belong in that row's unit.

**A term is applied or refused by name, never dropped.** Each new unit declares what it enforces in
`internal/view`, and the route refuses the rest by name. A unit added to the interface and not to
the declaration silently widens somebody's scope.

**A page says what it is not showing.** The *Showing N of M* line, the pager and the server-side
ordering are load-bearing and already correct, checked against the API rather than against the
screen. Anything that changes what a unit offers leaves all three still agreeing with the endpoint
for the whole match.

**The Dashboard does not get a second job.** It is a way into Inventory. Every control on it names a
count it can produce and a scope it hands over; nothing on it exists because the page looked empty.

**A card names the scope that produced its number, and the two drift apart silently.** Four of the
five named ownership — *in what you run*, *actions in use*, *you depend on*, *routes from something
you own* — over figures taken across the whole corpus, and one card read the declared scope and
said **0**. Nothing could disagree while every seeded repository was also an owned one; a corpus
where they differ is what separates them. So a figure is bounded to the declaration or the caption
names the corpus it came from, and where a figure is bounded, *nothing declared* renders as the
word **none** rather than as a nought, which reads as a corpus examined and found clean.

## Verification

- Every action a unit offers applies to every row that unit can list, checked by pressing it on
  each unit rather than by reading the markup.
- No action that belongs to the scope rather than the unit moves into a unit, and none that belongs
  to a unit survives switching away from it.
- The bill of materials reaches the reader by a route the navigation admits exists, or its
  statements reach them somewhere that is in the navigation.
- Driven in a real browser against a populated corpus, in both colour schemes — every defect this
  plan has closed was found by looking, and none of them by a green build.
