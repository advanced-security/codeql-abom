# Vocabulary

One word per notion, chosen once and used everywhere. `checkVocabulary` in
`ui/tools/check-style.mjs` fails the build on the retired spellings, so this is a build step rather
than a convention.

## Learning

**Tutorial** enables a read-only walkthrough of existing pages. **Completed** in the home
learning checklist means the walkthrough was finished, never that the operational task was
performed. Missing data is explained in the current step without inventing evidence.
The **ABOM Tutorials** section on Home and Settings uses play and restart controls;
titles can also resume an unfinished lesson in the current tab. Acknowledgement dismisses the
first-visit welcome.

**Demo** is an instance serving **synthetic training data**: fictional repositories on a
**simulated GitHub**, with **real CodeQL results** computed over them. **Practice in this Demo**
ends a query lesson and opens the page to do its steps for real. Demo is never a toggle.

## Ownership

Three notions, and only one of them means responsibility.

```mermaid
flowchart LR
  DECL["Yours<br/>abom_owned_scope"] --> RESP{"can you<br/>change it?"}
  MEMB["A member of<br/>abom_account"] --> RESP
  REACH["Reached<br/>the graph"] --> RESP
  RESP -->|yes| Y["only what you declared"]
  RESP -->|no| N["membership proves nothing;<br/>reached is somebody else's"]
  style MEMB fill:#3d2d1d
  style REACH fill:#3d2d1d
```

| Word | Source | Means |
| --- | --- | --- |
| **Yours** | `abom_owned_scope` | Declared by the operator. The only one that implies you can change anything. |
| **A member of** | `abom_account` | What `gh` reports. Decides nothing on its own. |
| **Reached** | The graph | Whatever the declared scope depends on, at any depth. |

**Origin** distinguishes your declared repositories from other recorded dependencies.
Only the ownership declaration establishes what is yours; the filter displays these labels
rather than internal storage values.

| Word | Means |
| --- | --- |
| **owned** | Your declaration covers the repository it lives in |
| **starting point** | A directly recorded repository outside your ownership declaration |
| **upstream** | In a repository read to resolve another repository's dependencies |
| **external** | A referenced item not recorded inside a repository |

**What you own** is the `owned` scope: only what your declared repositories hold, so every row and
node it lists reads *owned*. **Yours and what it runs** is the `supply-chain` scope: that, and
everything it runs at any depth, so it lists external actions because yours run them. Each named
scope carries a description, shown wherever it is offered; a kept scope is described by what its
filter asks.

**Retired, and not to be reintroduced:** *in scope* and *under review*. Both read as a claim of
responsibility that nobody made. There is no state in which a repository is under review by nobody
— nothing happens until an operator claims an account.

**Both halves, always.** The declared scope is stated first, because every other figure is scoped by
it, and the count of what has been read stays beside it as *coverage*. "N repositories analyzed" is
a fact about how much has been looked at and claims nothing about who owns them; hiding it would
make coverage unreadable, which is the same mistake in the other direction.

**An absent claim is not a measured emptiness.** The strip reads "none declared", never "0".

## Boundaries

Every edge carries a stored class. The reader-facing words follow the Bill of Materials, because
that is the surface where a non-expert reads them.

| Stored | Shown |
| --- | --- |
| `same-repository` | Same repository |
| `same-owner` | Same account |
| `cross-owner` | Another account |

Stored values do not change. **Retired spellings:** *Same org/owner*, *Cross org/owner*, *Same org*.

A reader who filters "Another account" on one page and "Cross org/owner" on another has no way to
know they asked the same question — which is the whole reason one spelling wins.

## Exposure

| Word | Means |
| --- | --- |
| **Runtime** | This organization executes it |
| **Build time** | Only an upstream project's CI runs it |
| **Direct** | The repository names it itself, `hops = 1` |
| **Transitive** | Reached through an intermediary someone else maintains |
| **Reached from yours** | The `reached` term: charged to the repositories that reach it, directly or transitively, rather than to where a finding was found |
| **Reference** | A version exactly as a workflow wrote it — `v4`, a branch, a commit — and the `at` term that picks one. `v4` is not `v4.2.2`, even where both name one commit today. **Reference style** is only its shape |
| **Local path** | A `uses: ./…` with no reference. It runs at its caller's own commit, so it is not a version: never listed among the versions in use, never counted in **Distinct refs**, and never the default branch |
| **Depending** | What in the starting points runs it, directly or transitively, counted at one level: **steps**, **jobs**, **workflows** (files), **repositories**, or **groups** (one per value of the grouping property) |
| **Commit it runs** | What a reference resolves to, or what a lock selects behind it. **Not known** where nothing recorded one |
| **Through** | What a repository itself names that brings a dependency in, for a transitive use |
| **What `uses:` this** | The details page's table: one row per repository, reference and commit, read from the walk reach takes |
| **If this tag changes** | Who would run a different commit if one reference moved: directly, through an intermediary, and who a lock keeps where they are. A commit does not move |
| **Advisory severity** | The severity GitHub published an advisory at: critical, high, medium (GitHub also says moderate), low, or **unrated** where it published none. Never whether a reference falls inside the range |
| **Any of these policies** | A superset: a policy naming others, which refuses what any of them refuses |

Classification comes from `edge_kind`, which CodeQL already emits, and is never re-derived from
file paths. Reporting build-time work as though this organization ran it would overstate exposure.

## Coverage

| Word | Means |
| --- | --- |
| **Analyzed** | A query ran against it and the result was ingested |
| **Unanalyzed** | Known to exist, nothing has looked |
| **Nothing to analyze** | Looked, and there was no database or no workflow |
| **Not claimed** (queued) | Queued for a repository nothing owned reaches any more. The crawl does not analyze it and it holds no depth open, until a prune removes it |
| **Waiting for code scanning** | A push to a repository you own, held until code scanning's own analysis of that commit is reported or its wait runs out, so its database and findings can be taken rather than built. Still unanalyzed, and counted as outstanding |
| **Missing** (from a full listing) | A declared repository the last full listing of its account did not name. No longer yours, and nothing removed for it; what only it reached is what the prune offers. Only a full listing can say it, and only one can say nothing is missing |

A reader must be able to tell "we have not looked" from "there was nothing to look at", because
only one of them is work somebody can do. Partial coverage is its own state, not a rounding of
`analyzed` in either direction.

## Actions code scanning

| Word | Means |
| --- | --- |
| **Actions scanning** | Whether code scanning analyzes the repository's Actions code: through **default setup**, **advanced setup**, **both setups**, or **off**. **No Actions code** where the listing and GitHub found none; **not read** where nothing has looked. The setup and the database are written under it, in the same column |
| **Setup** | Which code scanning setup GitHub reports: **default setup**, **advanced setup** (a CodeQL workflow it reaches, or a CodeQL analysis default setup did not make), **setups in conflict**, **no setup**, or **setup unreadable** where GitHub refused the read. Left out where the scanning state already names it |
| **Actions database** | Whether code scanning holds an Actions CodeQL database ABOM can pull: **Actions database**, **no Actions database**, or **database not read** |
| **Covered** | Default setup already analyzes `actions` |
| **Add** / **Enable** | The only classes ABOM writes: add `actions` to default setup, or turn it on for `actions` alone |
| **Blocked by an enforced configuration** | An enforced configuration turns default setup off; GitHub refuses changes there |
| **A language the write refuses** | Default setup analyzes a language GitHub's write rejects, such as `rust`, so `actions` cannot be added without removing it |
| **Not crawled yet** | Holds Actions code ABOM has not analyzed, so advanced setup cannot be ruled out |
| **Preview** | What a batch would write, from what was last read; it asks GitHub nothing |
| **Approved by** | The write token's owner, whom GitHub records for every write |
| **Batch** | An approved preview's writes, one repository at a time, each read back |
| **Write targets** | What `--enable-targets` and `--enable-accounts` let a server write; without them it only previews |
| **Not a write target** / **Left out by name** | A preview row outside the write targets, or excluded by name; never written from that preview |
| **Ledger** | Every step a write took: refused, skipped, deferred, sent, verified or failed |

A class is what the last reading said, never a verdict on the code: **covered** says default setup
analyzes `actions`, not that what it found was safe.

## Locks

| Word | Means |
| --- | --- |
| **Locked to** | The commit a current Actions lockfile selects behind a declared reference: what runs, beside what the source says |
| **Locked by** | Whose current lock selects a queued commit. A caller's lock is named with the pin it reached the composite through, and chooses for that caller only |
| **No lockfile** | Read at the commit the repository resolves to, and there is none |
| **Not in its lock** | A lockfile is there and does not list these uses |
| **Lockfile unsupported** | A lockfile is there and this reader refused it, by name; it selects nothing |
| **Lockfile not read** | Nothing read the lockfile at that commit. Unknown, not unlocked |
| **Check lock** | Run GitHub's `gh actions-lock` verification of a repository's current commit, no-fix, once, because somebody asked |
| **Passed** / **Warnings** / **Failed** | What GitHub's verification said: nothing to report, findings below an error, or the lock called invalid |
| **Inconclusive** | The verification reported a category this build does not know, or could not confirm reachability. Not a pass |
| **Error** (verification) | The tool gave no report, or changed what it was reading. Says nothing about the lock |

A lock is evidence about what a workflow runs, never a verdict that it is safe. The declared
reference stays on every count as written; what a lock selects is a second answer beside it.

## Analysis modes

| Word | Means |
| --- | --- |
| **Regular** | One repository's own checkout, the input shape ordinary Default Setup analyzes |
| **Extended** | An owned repository plus staged dependencies, which ordinary Default Setup does not stage |
| **Mixed** | One defect observed in both shapes, counted once; not a writable mode or a filter value |
| **Shape not recorded** | No recorded run establishes the finding's shape |

A mode describes inputs, not whether GitHub enabled a query or will publish an alert. Coverage
and dependency reach continue to describe regular analysis even when a view selects extended
evidence. Neither a lock context nor a query's source substitutes for this database shape.

## Queries

| Word | Means |
| --- | --- |
| **Query version** | A content hash over a query's text and every library it imports. Editing a library changes it |
| **Source** | Which pack supplied the query: upstream, ABOM's own, custom (the operator's), or several at once. A finding's source is its rule's, as the evaluation that reported it recorded |
| **Source not recorded** | No evaluation that reported a finding recorded where its rule's query came from. It matches no source, and is counted beside an answer narrowed by one |
| **Provider not recorded** | A finding whose SARIF named no tool, which is most findings older than the column. It matches no provider, is never relabeled CodeQL, and is counted beside an answer narrowed by one |
| **Severity** | The level a result reported — error, warning, recommendation. A rule's own metadata, never ABOM's judgement |
| **Security severity** | A rule's declared security score banded as code scanning bands it: **critical** from 9.0, **high** from 7.0, **medium** from 4.0, **low** above zero. Every count of findings says how many sit in each band. A rule's own metadata, never ABOM's judgement |
| **Unknown** (security severity) | A finding whose rule declares no security score. Counted beside the bands, never as low or as zero |
| **Declared metadata** | Every pair a query version states about itself, as `codeql resolve metadata` reads it. *Not read yet* means nothing has resolved that version, not that it declares nothing, and lasts only until the server reads it. *Cannot be read* means no file on disk is that version; *cannot be known* means no version was recorded for the runs |
| **What reported it** | A finding's evaluations: what ran, in which shape, under which CodeQL, the rule's own version, and who ran it — the crawl, run *N*, the closure of an owned repository, or a replayed recording |
| **Evaluated by** | Who produced an evaluation's result for the rule: ABOM, or *code scanning analysis N* where the repository's own code scanning analysis of the same commit ran the rule. A rule id names one query whatever pack or suite ran it, so the CodeQL version and pack shown are then that analysis's, and no query version is recorded |
| **Lane** | What ran that is not one query: a whole suite invoked in one call, or several queries in one evaluation |
| **Stage** | How far a version has been taken: `dev`, `test`, `extended-test`, `review`, `deploy` |
| **Step** | One of seven marks inside `dev` and `test`: target, help, expected results, test database, AST, compiles, tests. A done step says what was done: chosen, written, extracted, printed, compiles, pass. Otherwise failed, unconfirmed or not yet. Not yet is grey and is never a failure |
| **Outcome** | Where a request stands as a whole, read from its steps: an agent working on it (and the first step not yet passed), a failed or unconfirmed step, not started, unfinished at a step, or ready |
| **Mark** (in a fixture) | `# $ Alert` on a line the query must report. A marked line it does not report is a **missing** result, and an unmarked line it reports an **unexpected** one |
| **Failed first** | A version's tests failed on a marked result, over the fixtures a later version passes on. Without it the tests step is a warning |
| **CodeQL *x*** | The CodeQL a test, a tree or a run was recorded under. A mark recorded under another reads as stale, and *not recorded* is not a version |
| **Request** | What a query should catch or stop catching, with at least one `COMPLIANT` and one `NON_COMPLIANT` example. Open, in progress, done or declined. A query id the catalog holds asks to **improve** it; any other valid id asks to **create** it |
| **Linked example** | An example read from a GitHub link to lines, kept with the commit it was read at, so it does not change when the branch moves |
| **Rule tag** / **not rule tag** | Keep, or drop, findings of rules declaring this exact tag, such as `external/cwe/cwe-829`. Matched exactly and case-sensitively, never as a prefix. A request's CVE and CWE references become such tags |
| **Selected by you** / **decided by the agent** | Who chose a request's target. Empty means the agent decides |
| **False alarm** / **missed detection** | Evidence on a request: a finding that should not have fired, or a repository where nothing fired that should have |
| **Session** | An agent writing the query a request asks for, started by a person. Running, finished, stopped, failed or interrupted |
| **Audit log** | The shared, paged record of observed agent lifecycle events, permissions, decisions and measured evidence, opened from the Audit log icon on any request or run |
| **AI credit cap** | The most AI credits Copilot lets one session spend, when one is set. With none set, Copilot's own plan limits still apply |
| **Time limit** | How long a session may run before it ends; shown beside how long it took, or has taken so far |
| **Event / call** | A recorded observation / a runtime invocation with an exact supplied identity. Ordinary SDK pre/post hooks cannot be paired just by name or time; event totals are not call totals |
| **Effective policy** | The immutable limits, tools and file/fixture restrictions captured from the configuration and bounds actually used. Later settings do not repaint an older session |
| **Not recorded** | The historical value or observation was not captured. Distinct from zero, a recorded absence of an AI credit cap, or proof no activity happened |
| **Reproduction evidence** | Ordered policy, input identities, decisions and measured test iterations. Digests and links are not source archives, and missing/changed inputs prevent exact reproduction |
| **What it did** | A request's entry into the same complete audit event reader, including uncorrelated observations and refusals |
| **Refused** | A tool call that ABOM's bounds did not let run, or a run that could not start. Either says why |
| **Tree** | What `PrintAST` printed over a test database, in a session or by ABOM when somebody asked. It is the evidence for the AST step while the fixtures are unchanged |
| **Expected results** | The `.expected` a query's test compares against, written by the agent from the help and the fixtures' marks before the query. Never the query's own output |
| **Draft** | A request an agent drafted from a run's picked results. Nobody has filed it until a person saves it |
| **AI units** | What a session spent, as the Copilot CLI reported it. Absent where it reported nothing |
| **Run** | A request to analyze chosen query versions over chosen repositories' databases, or over every repository a scope lists. Drafted on the Inventory or the CodeQL page, one draft for both; from the CodeQL page, over a named scope, whose name the run keeps beside the question it resolved to |
| **Still to do** | What a run under way has not reached in this start: waiting, running, or finished only before this start began. Listed first, the one running at the head |
| ***About x left*** | A run's time remaining: its own finished members' pace times what is still to do. Absent until one of them finishes, and never forecast before Run |
| **Ready to query** / **the run builds first** / **never analyzed** | Before Run, whether each repository's database is already on disk, built by this CodeQL, or must be extracted. One another CodeQL built is counted with those the run builds. One ABOM has never analyzed names no commit, so the run tries its default branch, which may hold nothing to analyze |
| **Findings on** / **findings off** | A run's one setting: whether what its queries report is stored as findings or held. Suggested from what its queries are — on only when every one is a version a pack published or a suite promoted — and overridden by one switch |
| **Held** | What a run with findings off reported, kept apart from findings under an evaluation no skip reads. It states first how many of the scope's repositories it covers |
| **Convert** | Make findings of what a held run kept — all of it, one repository, or chosen results — recording who did it and from which run |
| **Skipped** | A run's repository already answered by an earlier run with the same versions, commit and CLI, and carrying that answer |
| **Interrupted** | A run the server stopped under: neither finished nor canceled |
| **Comparison** | A finished regular run's results set against the code scanning alerts GitHub holds now. It reads and writes nothing back |
| **Would add** | What adopting a run's queries would put in front of developers: **new** results no alert stands for, and results **reopening** a fixed alert. Never the run's total |
| **Already alerted** | A result an open or dismissed alert already stands for, by rule and place. It adds nothing |
| **Not compared** | A repository a comparison names and leaves out of its count, with why: no results, results counted before their evaluation was kept, no code scanning analysis, no Actions analysis, or not read |
| **Measured** / **an inference** | A repository analyzed at the commit and under the CodeQL GitHub's alerts describe, or one where either differs |
| **Current**, **behind**, **ahead**, **diverged**, **unknown** | How the commit analyzed relates to the default branch when a comparison read it. Unknown where the branch could not be read, never current by default |

A query id names a rule; a query *version* names the code behind it, which is what a stage, a test
result and a run all attach to. Source comes from the pack, never from the id: a prefix is a naming
convention anybody can adopt. *Custom* is authorship; *yours* is ownership, and the two never
share a word.

Advancing a stage is an act somebody signs, never something ABOM awards. `test`, `extended-test`
and `review` are the rungs that are earned — the first requires that version's own tests to
pass, the second every step but the target, which belongs to a request, and the third a
comparison of a run of that version under the CodeQL the server runs — and `deploy` records intent
until the evidence behind it exists.
A warning is evidence gone stale, never a guess.

## Enablement

**Coverage** is whether ABOM looked. **Enablement** is whether the product feature is switched on,
configured for the languages present, running, and running recently. They are different questions
with different owners, and answering one does not answer the other.

An enablement status is read from an external audit and is an attributed external statement, in the
same class as a GHSA. It explains a coverage gap; it never becomes an ABOM verdict, and it is never
rendered as though ABOM measured it.

**Not to be used as synonyms for each other**, because "unanalyzed" is work ABOM can do and
"not enabled" is work the repository's owner has to do first.

## Named scopes

| Word | Means |
| --- | --- |
| **Named scope** | A predicate with a name: one of ABOM's built-in questions, or one an operator kept. It opens on any page that applies its terms |
| **Keep** | Name the scope on screen and save it. On the Explorer, how it is drawn is kept beside it |
| **Forget** | Delete a scope an operator kept. A built-in cannot be forgotten |
| ***Page* cannot narrow by** | Why a named scope is disabled on this page: the terms it cannot apply, and the pages that can |
| **This graph does not apply *x*** | The Explorer arrived with a term it applies on neither side, and draws nothing rather than a wider picture |

A named scope is the question, never the page it was kept on: one kept on the Explorer asks the
same thing on the Inventory, and one kept in the CodeQL page's run draft is the scope the draft was
over.

## Scope inside an organization

| Word | Source | Means |
| --- | --- | --- |
| **Repository grouping** | A repository custom property GitHub holds | A distinct value of a configured key, used to group and filter the estate. ABOM names the key the operator chose rather than what it might mean: `business_id` yields business units, `team` yields teams, and asserting either from the other would be a meaning read from nowhere |
| **No value** | The absence of that property | Its own state, reported as such |
| **Property scope** | A declaration's terms on custom properties | The repositories of a restricted organization carrying every listed value, kept current as GitHub's values change. Several on one account each count. **Held** while the organization's schema is unread or lets repository administrators set the property: kept, counting nothing. **Not judged** where a repository's values did not arrive whole, which is never a mismatch |
| **The organization's default** | A property's default in the schema | What every repository that never chose a value carries. A property scope naming it takes in each of them, and every repository created later |

The key or keys are configured by the operator — `business_unit`, `app_name`, whatever the
organization already administers. The *values* are discovered, never listed by hand.

A repository carrying no value for the configured key is **never folded into a default bucket**, for
the same reason an absent measurement is not a zero: somebody has to be able to see that the
property was never set.

## Waivers

Two notions, and the difference is whether the work happens and whether it ends.

| Word | Means | Effect | Required shape |
| --- | --- | --- | --- |
| **Exclusion** | Do not do this work | Nothing is analyzed, nothing is recorded | Enumerated |
| **Exception** | Do the work, do not act on the result | The work runs and records; the result is waived, or hidden | Enumerated, and expires |

They govern the estate, not only a rule: excluding a repository from analysis is not writing a
policy. Each carries a reason nobody may omit and an author, and an exception carries the date it
comes back.

**Hiding is not destruction.** An exception filters a read. Pruning removes what the declared
closure no longer reaches, which is a different operation with different consequences, and neither
is a synonym for the other.

Both must be listable in full at any moment. **Neither is ever an open-ended pattern**, because a
pattern cannot be audited and silently covers a repository created after it was written — a waiver
nobody granted.
