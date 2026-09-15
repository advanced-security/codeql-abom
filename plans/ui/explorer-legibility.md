# The Explorer draws hundreds of points and names none of them

**Status:** correct, disclosed, and unreadable at the scope it opens on.

## Why this exists

At 1440×900 the Explorer opens on **395 of 2,639 nodes** drawn into a 907×536 canvas, with no
labels at any zoom and most nodes one or two pixels across. Nothing is wrong with the data and
nothing is hidden: the view states its own truncation, the legend gives every node kind a shape as
well as a color, and the panel says plainly that starting points and upstream are not colored
apart. A reader still cannot name a single thing on screen without clicking it blindly.

This is the surface the whole project is named after. It is also the one place where being correct
is not enough to be useful, because the failure mode is not a wrong number — it is a picture that
carries no information and looks like it does.

Two smaller faults sit in the same view and are cheap to close while it is open. `WHO CAN FIX IT`
and `DISTANCE` are rendered disabled with no `title` and no `aria-label`, so two of five force
modes are dead with nothing saying what would enable them. And every page load logs
`Multiple instances of Three.js being imported`, which for a library that decides identity with
`instanceof` is a correctness risk in the 3D renderer, not only bundle weight.

## Pending

1. **The collapsed view draws almost nothing.** Grouping folds 2,639 nodes into 24 groups by owner
   or 8 by business unit, and the canvas then shows **two points and one edge**. The groups exist —
   the header counts them — so this is the camera rather than the fold: nothing refits the view to a
   set that just became two orders of magnitude smaller. It is the cheapest large win here, because
   folding is already the answer to the density problem and currently hides its own result.
   Confirmed identical under owner and unit grouping, so it is not specific to either.
2. **A default scope somebody can read.** The fork, and it is a product decision rather than a
   layout one: open at **repository granularity** so the first screen is tens of nodes rather than
   hundreds; open **focused on the highest-priority node** with one hop, making the landing view an
   answer rather than an inventory; or open **empty with a chooser**, which is honest but asks a
   first-time reader a question they cannot yet answer. Whether 3D stays the default belongs to
   this decision — it spreads an already sparse cloud across a third axis.
3. **A rule for when a label appears.** Labels are absent rather than clipped, so there is nothing
   to recover and no affordance suggesting there could be. The fork: a **zoom threshold**, labels
   on the **top N by reach** at any zoom, or **hover and selection only**. The first two put text
   on screen unprompted and can collide; the third keeps the view clean and leaves it mute.
4. **The disabled force modes say what they are waiting for.** Depends on nothing. A control that
   is visibly present and inert has to name its condition, in the control itself rather than in
   documentation nobody has open.
5. **One copy of Three.js.** Find what imports it twice — the bundled module and a transitive copy
   is the usual shape — and collapse it. The warning is the only evidence today; whether it has
   ever caused a misrendered frame is unmeasured, and it is cheaper to remove than to investigate.

## The constraints that must not be broken

**Scope is the fix, not styling.** When a force view cannot be read, the answer is fewer nodes —
coarser granularity, fewer hops, a tighter filter — never a prettier layout over the same set. A
denser view that has been restyled is the same view; it just takes longer to discover that.

**Truncation stays disclosed.** The view already says how many nodes it is not drawing and why.
Any change to the default scope keeps that line, because the version of this fix that shows fewer
nodes and stops admitting it is worse than the defect.

**Shape stays a channel beside color.** Node kinds are distinguishable without hue today. A
redesign that reaches for color alone fails for roughly one in twelve men, and passes every
automated check we have.

**A reader is never told that unlabeled means unimportant.** Whatever rule decides which nodes
carry text, the ones without it must not read as background. An absent label is a rendering
budget, not a judgement about the node.

## Verification

- At the default scope on a populated corpus, a reader names a node and its kind from the screen
  alone, without clicking and without zooming.
- One edge can be followed from end to end anywhere in the view.
- The screenshot pass in `.github/skills/verify-abom-ui/`, in **both** schemes: contrast is
  arithmetic and already enforced, but whether a view communicates is not.
- The browser console is clean on load, which it is not today.
