/**
 * Lockfile coverage for `uses:` references.
 *
 * Distinct from `ActionsLock.pins`: coverage asks whether the lockfile accounts for a reference
 * at all, matching `gh actions-lock --verify-local`.
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
 * Local, same-repository and container references are not recorded by `gh actions-lock`.
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
 * `./path` and `$/path` both target a file in this same checkout.
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
 * `gh actions-lock` keys shared-action references under each workflow that reaches them.
 */
private WorkflowFile reachingWorkflow(File f) { callsIntoRepository+(result, f) }

/**
 * A workflow file, including malformed ones that produce no `Workflow` entity.
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
 * Repository-level findings need a stable file location.
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
   * The entry's own `ref` may differ from `pin`; coverage only needs a resolved commit.
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
   * The ref must match what the workflow says.
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
   * Composite-action references must be covered under every workflow that reaches them; unreached
   * definitions have no workflow section to appear in.
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
