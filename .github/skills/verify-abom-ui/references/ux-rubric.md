# UX rubric

Judgements to make while looking at a capture. Each is falsifiable — cite the region of the
image that led to the verdict, so a reader can disagree with the evidence rather than the
opinion.

## Encoding

**Colour is never the only channel.** Severity distinguished by hue alone fails for roughly one
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
- Graph: can you separate one edge from its neighbours anywhere in the view?

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
  analysed" must not look alike.
- Is coverage visible without hunting for it?
- When the API reported truncation, does the view admit it?

## Motion

Only for the live Discovery view.

- Does movement carry information, or decorate? A node migrating from pending to analysed is
  information. A pulsing border is not.
- Can the eye follow a single node through the transition, or does the layout reshuffle?
- Does anything animate indefinitely, making the screen unrestful while idle?

## What is not a finding

Keep these out of the report unless they impede a decision: preference about spacing, colour
palette taste, font choice, whether a chart would look better as a different chart. The report
is more useful when everything in it is actionable.
