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
   * Holds if this lockfile accounts for `uses`: listed at the very ref written there, and
   * resolved to a commit.
   *
   * The ref must match what the workflow says. An entry left at `@v9.2.0` after the workflow
   * moved to `@v9.3.0` describes a commit nothing will run, which is not coverage.
   *
   * A composite action is matched against every workflow entry rather than its own path. The
   * `workflows` section is keyed by workflow, and an action reached through one contributes its
   * references to whichever workflows use it -- `gh actions-lock` reports a broken reference in
   * `.github/actions/setup-codeql` against all four workflows that use it, not against the
   * action.
   */
  predicate covers(LockableUses uses) {
    exists(string pin, string nwo, string ref, string workflowPath |
      this.lists(workflowPath, pin, nwo, ref) and
      calleeMatches(uses.getCallee(), nwo) and
      ref = uses.getVersion() and
      this.resolvesToCommit(pin)
    |
      workflowPath = uses.getLocation().getFile().getRelativePath()
      or
      exists(uses.getEnclosingCompositeAction())
    )
  }
}
