# A reference is known, or it is unknown in a way that can be named

**Status:** Unblocked for design, and item 3 is wider than it looked. Dataflow crosses a `uses:`
boundary at any depth once the callee is staged under `.github/actions/external/${owner}/${repo}/`,
so the flow item 3 needs is reachable through a closure database (`codeql-abom closure`,
[analysis modes](../../docs/architecture.md#analysis-modes)) rather than confined to a repository
that happens to appear once. What a closure cannot give it is two references to one dependency
inside one closure.

## Why this exists

`uses: ${{ env.ACTION_REF }}` is recorded exactly as written, and then it stops. ABOM has one
bucket for that, and the bucket holds several different facts:

- a reference built from a matrix whose values are literals in the same file;
- a reference built from an environment variable set two jobs earlier;
- a reference built from `github.event.inputs`, which is **an attacker choosing what runs**;
- a reference nothing can explain at all.

The first is enumerable, the third is a finding, and the fourth is a coverage gap. Collapsing them
reports the attack and the typo as the same thing. This is
[`db/unresolved-references.md`](../db/unresolved-references.md)'s missing half: that plan refuses to
invent a package for an unevaluated expression, correctly, and has nothing to say about what the
expression *is*.

The shape to copy already exists. The CodeQL cryptography work (CBOM) puts a shared typed model
between the extractor and the queries, binds concrete APIs into it through adapters, and keeps
inventory, policy and security queries as different readings of one model. Its recorded warning is
the one that matters here: canonical data must carry structured values, never display strings like
`<unknown>`.

## Pending

1. **Name the states.** Seven, each a distinct fact rather than a degree of confidence: known;
   unknown with no identified source; dynamic with local provenance; remote-derived; several
   candidates; known and unknown branches; not analyzed. Each carries where it was measured.
   Depends on nothing — this is arithmetic over a vocabulary.
2. **Store them as statements, not as a column.** `abom_observation` is already keyed by the
   statement rather than the run, already keeps both readings when a fact changes, and already
   refuses to pick a winner between two. That refusal is exactly what several candidates requires,
   so this is a naming exercise over a table that exists. Depends on 1.
3. **Resolve what dataflow can resolve.** `MatrixExpressionImpl.getLiteralValues()` and
   `getAMatrixVarExpr` already enumerate a literal matrix; an environment variable needs flow across
   a `uses:` boundary, which a staged closure provides. Each resolved candidate is a statement with
   its own provenance, never a replacement for the reference as written. **A dependency named at two
   references inside one closure can only be staged at one of them**, so a candidate resolved there
   is a statement about the staged reference and says so. Depends on 1; the closure database it
   reads is built.
4. **A remote-derived reference is reported as a finding, by a rule.** Not as a property of a node
   that the interface colours differently. Depends on 3.

## The shape the model has to allow, and the reason

Actions is one adapter, and it is the only one in scope. It is not the only one the model must be
able to hold, and the three that are visible from here are visible because each is a *different
extractor over the same tree*:

| Axis | Why it is another adapter |
| --- | --- |
| Dockerfile base-image declarations | The referenced Dockerfile needs fetching and a parser beyond the action metadata |
| A JavaScript action's own dependencies | `runs.using: node20` means the behaviour is in `index.js`, which needs the `javascript` extractor and a lockfile that is not checked out |
| A shell script a composite action calls | The library already parses **embedded** `run:` blocks; a referenced `./scripts/build.sh` is a separate file, unfetched and unparsed |

So an adapter declares four things: **the source inputs it needs fetched, the parser or language
extractor it uses, the queries or analysis it runs, and the node kinds it makes statements about.**
None of the three adapters is implemented by this plan. Declaration-only container work has its
own owner in [container-inventory.md](../db/container-inventory.md); image layers and installed
packages remain outside it. Literal declaration coverage can start without waiting for the
dataflow resolution in item 3, but it must use the same uncertainty and source-coverage contract.
JavaScript-action dependencies and referenced shell scripts remain separate, unplanned adapters.

## The constraints that must not be broken

**An adapter set is a property of a crawl, not a switch flipped afterwards.** Sparse patterns are
chosen when a repository is fetched. Enabling an adapter later does not analyze what was already
fetched — it silently reports nothing for it, which is indistinguishable from nothing being there.
Turning one on means re-fetching, and the interface has to say which adapters produced the corpus
it is showing.

**Uncertainty is structured data.** No `<unknown>`, no sentinel string, no concatenated property.
A reader asking which references are remote-derived is asking a query, not doing a substring match.

**A candidate never displaces the reference as written.** `actions/checkout@v7` is evidence about
what the organization runs even where upstream publishes no such version. A resolved candidate is
an additional statement beside it.

**A security verdict still comes only from a rule id.** *Remote-derived* is a measurement. That it
is bad is `actions/...`'s call, and this plan adds no verdict of its own.

## Verification

- Each of the seven states is exercised by a fixture that produces exactly that state, built from a
  tree and run through the CLI.
- Two candidates for one reference produce two rows, and no pass anywhere picks one.
- A second run classifies nothing new, so the derivation settles.
- The remote-derived case is checked against a workflow that really does build `uses:` from
  `github.event`, because a synthetic one proves the query compiles and nothing else.
