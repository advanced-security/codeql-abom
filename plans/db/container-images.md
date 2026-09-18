# A container image is not an account

**Status:** A container image is a node, an edge and a bill-of-materials component, and the graph
groups it under an account that does not exist.

## Why this exists

`uses: docker://alpine:3.20` is a real dependency and ABOM records it correctly as far as identity:
`ParseUses` gives it `NodeDockerImage`, marks it external, keeps the tag as the reference value,
and `NodeKeyIn` returns `docker://alpine:3.20`. Nothing sets `Owner`, because an image has none.

`graph_org.go` then groups external nodes by account, and for a node with no parsed owner it falls
through to splitting the key on its first slash. For `docker://alpine:3.20` that slash is the
second character of `//`, so the account is the literal string `docker:`. Every container image in
the corpus is drawn inside a boundary labelled with it, and `docker://registry.local:5000/tools/build`
lands there too.

**The function already documents the right answer.** `ownerOf` says it "reports the account a node
belongs to, and empty when it belongs to none" — and an image belongs to none. The fall-through
contradicts the contract written above it.

**This is a fall-through that has already produced a wrong answer once.** The same first-slash
split on a self reference (`$/…`) once had ABOM reporting dozens of third-party dependencies on an
account named `$`. A reference whose syntax ABOM understands perfectly well should not be reaching
that branch at all.

`docs/vocabulary.md` says grouping is by **account**, and an account is something an operator can
be a member of and declare ownership over. `docker:` is neither, and a boundary drawn around it
invites a reader to believe there is an account there to go and look at.

## Pending

1. **An image has no account, and the grouping says so.** A node with no owner belongs to no
   account rather than to a synthesized one. The Explorer withholds a boundary it cannot honestly
   draw and says how many it withheld, which is behaviour it already has for boundaries that would
   enclose somebody else's node. Depends on nothing.
2. **Confirm the blast radius against the crawled organization** before and after: how many nodes
   sat under `docker:`, and which surfaces rendered it. The grouping feeds the Explorer, and the
   same `ownerOf` result may reach a filter or a count that has been quietly wrong for as long.
   Depends on nothing, and is worth doing first.
3. **Refuse the fall-through rather than fixing its output.** The branch exists for "anything else
   without a parsed owner", and container images are the only known member. Once they are handled,
   what remains is a reference ABOM did not understand, and synthesizing an account for one of
   those is how the `$` bug happened. Depends on 1.

## Deliberately not in this plan

**Any modeling of what a container image contains.** The image's layers, its base image, its
packages, the Dockerfile behind `runs.using: docker` — all of it needs analysis ABOM does not do,
and would need the shared model and the adapter shape in
[`ql/semantic-model.md`](../ql/semantic-model.md), which is itself blocked on
[`ql/uses-resolution.md`](../ql/uses-resolution.md). This plan is the reference, not the image.

**A package-url for containers.** It was investigated and refused. The GitHub Advisory Database's
supported ecosystems are Composer, Erlang, Go, GitHub Actions, Maven, npm, NuGet, pip, Pub,
RubyGems, Rust and Swift; **containers are not among them**, and the dependency submission API
alerts only for supported ecosystems. So an OCI purl in a snapshot can never produce a Dependabot
alert, while a snapshot *replaces* GitHub's own analysis and has no delete endpoint — cost with no
return. `normalize.PackageURL` already returns `""` for `docker://` and is right to.

**Whether the bill of materials should carry purls at all.** It carries none, for any component:
`cycloneComponent` sets `BOMRef`, `Name`, `Version`, properties and a VCS link, and never the
`PackageURL` field. That is a larger question than containers and is not settled here.

## The constraints that must not be broken

**A relationship ABOM does not understand is not a relationship to an account.** Carried from
[`db/edge-facts.md`](edge-facts.md), which states it for the same reason. A reference whose syntax
is unrecognized is refused, never split on the first slash and treated as `owner/repo`.

**An absent account is not an account named for the absence.** No `unknown` bucket, no `external`
bucket, no `docker:`. A node with no account has none, and the interface says so.

**The reference is recorded as written.** `docker://alpine:3.20` keeps its tag. Nothing normalizes
it to a digest, and nothing drops the registry from a registry-qualified image.

## Verification

- Node, edge, reach, exposure, bill-of-materials component and pinning totals are unchanged: this
  changes how a node is grouped, not whether it exists. Snapshot with
  `sqlite3 abom.db ".backup .abom/before.db"` — `cp` does not read the WAL.
- The count of nodes previously grouped under `docker:` is recorded before the change, because it
  is the only evidence the fix did anything.
- A registry-qualified image and a bare one are both covered, since both reach the same branch.
- The Explorer states the number of boundaries it withheld, and the number moves by exactly the
  number of accounts that stopped existing.
