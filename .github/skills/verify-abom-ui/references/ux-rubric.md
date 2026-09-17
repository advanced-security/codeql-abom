# UX rubric

Judgements to make while looking at a capture. Each is falsifiable — cite the region of the
image that led to the verdict, so a reader can disagree with the evidence rather than the
opinion.

## Encoding

**Color is never the only channel.** Severity distinguished by hue alone fails for roughly one
in twelve men. Look for a second channel: a label, shape, position or ordering. Judge this by
imagining the image desaturated, or capture it and check whether the ranking survives.

**Position means one thing.** In any force view, ask what a node's location tells you. If the
honest answer is "risk, and also popularity", the view has two semantic forces and neither
reads.

**Size differences are legible.** If node size encodes blast radius, the difference between 200
dependents and 20 should be obvious at a glance. A near-linear radius scale usually is not —
the large nodes swamp everything.

## Density and legibility

- Labels: overlapping, clipped, or dropped without an affordance to recover them?
- Numbers: does anything appear truncated or rounded such that its meaning changes?
- Tables: is the most decision-relevant column visible without horizontal scrolling?
- Graph: can you separate one edge from its neighbors anywhere in the view?

## One visual vocabulary

Sizes, families and control shapes are defined once and taken from there: the type scale in
`ui/wwwroot/css/app.css` under `:root`, and the MudBlazor `Typography` block in
`ui/Services/ThemeService.cs`. `make style` enforces the mechanical half — no inline styles, no
literal colors, no literal font sizes, no heading outside `PageHeader`, no unsized icon.

What the checker cannot see, and a visual review must:

- Does the same role look the same on every page? A page title, a section heading, a help line
  and a table figure should each be recognisable as themselves anywhere.
- Does anything sit *between* two steps of the scale, so it reads as neither one thing nor the
  other?
- Do controls of the same kind share a size and density across pages, or does one page's filter
  bar sit taller than another's?
- Do dark and light both hold? Contrast that works on one can vanish on the other, and only one
  of them is usually captured.

## Hierarchy

Look at the capture for two seconds and note what drew the eye first. On the landing page that
should be the highest-priority remediation work. If chrome, navigation or a decorative chart won
that contest, the hierarchy is inverted.

## Honesty

This is where ABOM's interface differs from most security dashboards, and where a visual review
earns its keep.

- Does anything imply certainty the data does not carry? An advisory shown without its range and
  the observed refs is the common case.
- Is absence of evidence distinguishable from evidence of absence? "No findings" and "not
  analyzed" must not look alike.
- Is coverage visible without hunting for it?
- When the API reported truncation, does the view admit it?

## Empty states

An empty screen is a stage in a process, not an error. Judge it by whether a first-time reader
could act.

- Does it say *why* it is empty — nothing declared, nothing run yet, or a filter that matched
  none? Those are three different screens.
- Does it name the next action, and is that action reachable from here?
- After taking it, does something visibly change? Two empty screens in a row is the failure this
  section exists to catch.

## Motion

Only for the live Discovery view.

- Does movement carry information, or decorate? A node migrating from pending to analyzed is
  information. A pulsing border is not.
- Can the eye follow a single node through the transition, or does the layout reshuffle?
- Does anything animate indefinitely, making the screen unrestful while idle?

## What is not a finding

Keep these out of the report unless they impede a decision: preference about spacing, color
palette taste, font choice, whether a chart would look better as a different chart. The report
is more useful when everything in it is actionable.
