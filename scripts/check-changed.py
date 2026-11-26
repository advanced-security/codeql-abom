#!/usr/bin/env python3
"""Chooses what `make check` runs from what changed against main, and runs it as `make verify` does.

A change is every file that differs from the merge base with main, committed or not, and every
untracked file git does not ignore. Each file names the checks that can see it: Go code its package
and everything that imports it, the interface its component tests, the chart its renders. A file
nothing here can place runs `make verify` whole, so a selection can only ever be too wide.
"""

import os
import subprocess
import sys
from dataclasses import dataclass, field

# Order is verify's: the longest group starts first.
GROUPS = ["interface", "go", "postgres", "ql", "checks"]
CHECKS = ["check-corrupt", "check-versions", "check-release-version", "pins", "chart", "fmt", "vet",
          "lint", "script-test", "js", "style"]

# A change to any of these reaches every check.
EVERYTHING = {"Makefile", "go.mod", "go.sum", ".codeql-version"}
EVERYTHING_UNDER = ("testdata/",)

# Read by people, and by no check.
UNCHECKED = {"LICENSE", "NOTICE", "CODEOWNERS", ".gitattributes", ".gitignore"}
UNCHECKED_UNDER = ("docs/", "plans/", ".github/skills/", ".github/instructions/", ".github/agents/",
                   ".github/codeql/", "infra/tests/kind/")

STORE = "/internal/store"


@dataclass
class Plan:
    groups: dict = field(default_factory=dict)
    packages: list = field(default_factory=list)

    def add(self, group, *targets):
        have = self.groups.setdefault(group, [])
        have.extend(t for t in targets if t not in have)

    def specs(self):
        out = []
        for group in GROUPS:
            if group not in self.groups:
                continue
            targets = self.groups[group]
            if group == "checks":
                targets = [t for t in CHECKS if t in targets]
            out.append(group + ":" + ",".join(targets))
        return out


def package_of(path, dirs):
    """The Go package whose directory holds path, or its testdata; None when no package does."""
    d = os.path.dirname(path)
    while d:
        if d in dirs:
            return dirs[d]
        d = os.path.dirname(d)
    return None


def plan(changed, packages, dirs):
    """What to run for the changed paths, or None for everything.

    packages maps each Go package to the packages its code and tests import, transitively; dirs
    maps a package's directory, relative to the repository, to the package.
    """
    p = Plan()
    touched = set()
    for path in changed:
        top = path.split("/", 1)[0]
        if path in EVERYTHING or path.startswith(EVERYTHING_UNDER):
            return None
        if path == "infra/tests/kind/values.yaml":
            p.add("checks", "chart")
            continue
        if path in UNCHECKED or path.startswith(UNCHECKED_UNDER) or path.endswith(".md"):
            continue
        if path.endswith(".go") or top in ("cmd", "internal"):
            pkg = package_of(path, dirs)
            if pkg is None:
                return None
            touched.add(pkg)
            p.add("checks", "check-corrupt", "fmt", "vet", "lint")
        elif path.startswith("ui/tests/e2e/"):
            p.add("checks", "js")
        elif path.startswith(("ui/wwwroot/js/", "ui/tools/")):
            p.add("checks", "js", "style")
        elif path.startswith("ui/wwwroot/css/"):
            p.add("checks", "style")
        elif top == "ui":
            p.add("interface", "ui-test")
            p.add("checks", "style")
        elif top == "ql" or path == "codeql-workspace.yml":
            p.add("ql", "ql")
            if path.endswith(("codeql-pack.yml", "codeql-pack.lock.yml", ".qls")) or path == "codeql-workspace.yml":
                p.add("checks", "check-versions", "check-release-version")
        elif path.startswith("infra/k8s/") or path in ("scripts/check-chart.sh", "scripts/setup-helm.sh"):
            p.add("checks", "chart")
        elif path.startswith(("infra/docker/", "infra/qlaas-repo-template/")) or path == ".dockerignore":
            p.add("checks", "check-versions")
        elif path in ("scripts/check-versions.sh", "scripts/check-release-version.sh", "scripts/release-version.sh",
                      "scripts/upgrade-packs.sh", "scripts/update-release-version.sh"):
            p.add("checks", "check-versions", "check-release-version")
        elif top in ("scripts", ".devcontainer"):
            p.add("checks", "script-test")
        elif path.startswith((".github/workflows/", ".github/actions/")):
            p.add("checks", "pins")
        elif path in (".github/dependabot.yml", ".github/dependency-review-config.yml"):
            continue
        elif path == ".golangci.yml":
            p.add("checks", "lint")
        elif path == ".node-version":
            p.add("checks", "js")
        else:
            return None
    if touched:
        p.packages = sorted(pkg for pkg, deps in packages.items() if pkg in touched or deps & touched)
        p.add("go", "go-test")
        if any(pkg.endswith(STORE) for pkg in p.packages):
            p.add("postgres", "test-postgres")
    if p.groups:
        p.add("checks", "check-versions")
    return p


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def changed_since(base):
    names = set(git("diff", "--name-only", "--no-renames", base).split())
    names |= set(git("ls-files", "--others", "--exclude-standard").split())
    return sorted(names)


def merge_base():
    for ref in (os.environ.get("ABOM_CHECK_BASE"), "main", "origin/main"):
        if not ref:
            continue
        try:
            return git("merge-base", "HEAD", ref).strip(), ref
        except subprocess.CalledProcessError:
            continue
    return None, None


def go_packages():
    """Each package with what it and its tests import, transitively, and each package's directory."""
    # Resolved, as go list resolves them: /tmp is /private/tmp on macOS.
    root = os.path.realpath(os.getcwd())
    out = subprocess.run(["go", "list", "-test", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .Deps \" \"}}", "./..."],
                         check=True, capture_output=True, text=True).stdout
    packages, dirs = {}, {}
    for line in out.splitlines():
        path, directory, deps = (line.split("\t") + ["", ""])[:3]
        base = path.split(" ", 1)[0]
        if base.endswith(".test"):
            base = base[: -len(".test")]
        base = base.removesuffix("_test")
        packages.setdefault(base, set()).update(deps.split())
        if " " not in path and not path.endswith(".test"):
            dirs[os.path.relpath(os.path.realpath(directory), root)] = base
    return packages, dirs


def main(argv):
    # --plan says what would run, and runs nothing.
    only_plan = "--plan" in argv
    os.chdir(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
    make = os.environ.get("MAKE", "make")
    base, ref = merge_base()
    if base is None:
        print("check: no merge base with main, so everything runs", flush=True)
        return 0 if only_plan else os.execvp(make, [make, "verify"])
    changed = changed_since(base)
    if not changed:
        print(f"check: nothing differs from {ref} ({base[:12]})")
        return 0
    packages, dirs = go_packages()
    chosen = plan(changed, packages, dirs)
    if chosen is None:
        print(f"check: {len(changed)} files differ from {ref}, and one reaches every check, so everything runs",
              flush=True)
        return 0 if only_plan else os.execvp(make, [make, "verify"])
    if not chosen.groups:
        print(f"check: {len(changed)} files differ from {ref}, and no check reads any of them")
        return 0
    print(f"check: {len(changed)} files differ from {ref} ({base[:12]})")
    if chosen.packages:
        print(f"check: {len(chosen.packages)} Go packages: those changed and everything that imports them")
    print("check: the browser suite runs only in make verify", flush=True)
    if only_plan:
        print("\n".join(["check: " + spec for spec in chosen.specs()] + ["  " + pkg for pkg in chosen.packages]))
        return 0
    env = dict(os.environ, ABOM_GO_PACKAGES=" ".join(chosen.packages),
               ABOM_VERIFY_LOGS=os.path.join(os.environ.get("ABOM_TMP", ".abom"), "check"))
    os.execvpe("scripts/verify.sh", ["scripts/verify.sh", *chosen.specs()], env)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
