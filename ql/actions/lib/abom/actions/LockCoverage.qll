/**
 * Lockfile coverage for `uses:` references.
 *
 * Distinct from `ActionsLock.pins` in `codeql/actions-all`, which asks whether a reference is
 * *pinned*. Coverage asks whether the lockfile accounts for it at all, which is what
 * `gh actions-lock --verify-local` reports and what a pinning rule cannot: that rule exempts the
 * `actions` and `github` owners outright and accepts any bare SHA, so a reference those cover is
 * never examined.
 */

private import actions
private import codeql.actions.Lock
private import codeql.actions.ast.internal.Yaml
private import codeql.files.FileSystem

/**
 * Holds if `callee` is `pinnedNwo` or a sub-action of it, as a repository pin also covers
 * `actions/cache/save`.
 */
bindingset[callee, pinnedNwo]
private predicate calleeMatches(string callee, string pinnedNwo) {
  callee.toLowerCase() = pinnedNwo.toLowerCase()
  or
  callee.length() > pinnedNwo.length() and
  callee.toLowerCase().prefix(pinnedNwo.length() + 1) = pinnedNwo.toLowerCase() + "/"
}

/**
 * A `uses:` a lockfile is expected to account for.
 *
 * Local (`./`), same-repository (`$/`) and container (`docker://`) references are excluded: the
 * first two resolve from the checkout itself at the commit already being run, and `gh
 * actions-lock` records neither them nor images.
 */
class LockableUses extends Uses {
  LockableUses() {
    exists(string callee | callee = this.getCallee() |
      not callee.matches("./%") and
      not callee.matches("$/%") and
      not callee.matches("docker://%") and
      exists(callee.indexOf("/"))
    )
  }
}

/**
 * Gets the file a same-repository `uses:` names.
 *
 * `./path` and `$/path` both identify either a directory holding an `action.yml` or a reusable
 * workflow file, and neither carries a ref, so the target is a file in this same checkout.
 */
private File sameRepositoryTarget(Uses uses) {
  exists(string path | path = uses.getCallee().regexpCapture("^[.$]/(.+)$", 1) |
    result.getRelativePath() = path
    or
    result.getParentContainer().getRelativePath() = path and
    result.getBaseName() = ["action.yml", "action.yaml"]
  )
}

/** Holds if `caller` contains a same-repository `uses:` naming `callee`. */
private predicate callsIntoRepository(File caller, File callee) {
  exists(Uses uses |
    uses.getLocation().getFile() = caller and
    callee = sameRepositoryTarget(uses)
  )
}

/**
 * Gets a workflow file that reaches `f`, directly or through other same-repository definitions.
 *
 * What runs an action is the workflow that reaches it, however many composite actions sit in
 * between, and that workflow is the key `gh actions-lock` files the action's references under.
 */
private WorkflowFile reachingWorkflow(File f) { callsIntoRepository+(result, f) }

/**
 * A workflow file, identified by where it sits rather than by what the extractor built from it.
 *
 * Not `Workflow`: a file under `.github/workflows/` that declares no `jobs:` produces no
 * `Workflow` entity at all, and a repository whose only workflow is malformed still has one.
 * Directly in the directory and not below it, because GitHub does not run workflows from
 * subdirectories even though the extractor still indexes them.
 */
class WorkflowFile extends File {
  WorkflowFile() {
    this.getParentContainer().getRelativePath() = ".github/workflows" and
    this.getExtension() = ["yml", "yaml"]
  }
}

/** Holds if this repository keeps a lockfile, whatever state it is in. */
predicate hasLockfile() { exists(AbomActionsLock lock) }

/**
 * The workflow file a repository-level finding is reported against.
 *
 * A repository is not a thing a query can select, so the alert lands on one file and the lowest
 * path is chosen to make that file the same on every run.
 */
WorkflowFile firstWorkflowFile() {
  result = min(WorkflowFile f | | f order by f.getRelativePath())
}

/** A `.github/workflows/actions.lock` file, viewed as a coverage record. */
class AbomActionsLock extends ActionsLock {
  /** Holds if the `workflows` section lists `pin` as `nwo@ref` under `workflowPath`. */
  pragma[nomagic]
  private predicate lists(string workflowPath, string pin, string nwo, string ref) {
    exists(YamlSequence pins, YamlScalar pinNode |
      this.(YamlMapping).lookup("workflows").(YamlMapping).lookup(workflowPath) = pins and
      pins.getElement(_) = pinNode and
      pin = pinNode.getValue() and
      nwo = pin.regexpCapture("^([^/@:]+/[^/@:]+)@([^:]+)$", 1) and
      ref = pin.regexpCapture("^([^/@:]+/[^/@:]+)@([^:]+)$", 2)
    )
  }

  /**
   * Holds if `pin` has a dependency entry carrying a full commit digest.
   *
   * The entry's own `ref` field is deliberately not compared against the ref in `pin`. They
   * disagree whenever an entry is keyed by commit and narrowed to a tag -- this repository's
   * `actions/cache@55cc8345...` records `ref: 'v6.1.0'` -- and that entry resolves a commit, so
   * it covers. Requiring them to agree is what makes `ActionsLock.pins` reject it.
   */
  pragma[nomagic]
  private predicate resolvesToCommit(string pin) {
    exists(YamlMapping dependency |
      this.(YamlMapping).lookup("dependencies").(YamlMapping).lookup(pin) = dependency and
      dependency
          .lookup("commit")
          .(YamlScalar)
          .getValue()
          .regexpMatch("^(sha1-[A-Fa-f0-9]{40}|sha256-[A-Fa-f0-9]{64})$")
    )
  }

  /**
   * Holds if the section for `workflowPath` accounts for `uses`: listed at the very ref written
   * there, and resolved to a commit.
   *
   * The ref must match what the workflow says. An entry left at `@v9.2.0` after the workflow
   * moved to `@v9.3.0` describes a commit nothing will run, which is not coverage.
   */
  pragma[nomagic]
  private predicate accountsFor(string workflowPath, LockableUses uses) {
    exists(string pin, string nwo, string ref |
      this.lists(workflowPath, pin, nwo, ref) and
      calleeMatches(uses.getCallee(), nwo) and
      ref = uses.getVersion() and
      this.resolvesToCommit(pin)
    )
  }

  /**
   * Holds if this lockfile accounts for `uses` under every workflow that runs it.
   *
   * A reference written in a workflow is looked up under that workflow. A reference written in a
   * composite action is looked up under each workflow that reaches the action, because the
   * `workflows` section is keyed by workflow and the same shared action contributes its
   * references to every caller separately -- an entry present for one caller says nothing about
   * another whose section omits it.
   *
   * A definition no workflow reaches is nothing this lockfile can be expected to list: `gh
   * actions-lock` walks out from the workflows, so unreached code has no section to appear in.
   */
  predicate covers(LockableUses uses) {
    this.accountsFor(uses.getLocation().getFile().getRelativePath(), uses)
    or
    exists(uses.getEnclosingCompositeAction()) and
    forall(WorkflowFile caller | caller = reachingWorkflow(uses.getLocation().getFile()) |
      this.accountsFor(caller.getRelativePath(), uses)
    )
  }
}
