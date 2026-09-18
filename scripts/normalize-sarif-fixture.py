#!/usr/bin/env python3
"""Strips machine-specific and volatile data out of a CodeQL SARIF fixture, in place.

A fixture is committed, so anything in it that differs between two machines shows up as a diff
nobody made: the CLI records the absolute path of every pack it resolved, which is a home
directory and a checkout layout, and it stamps each invocation with a wall-clock time.

Only fields ABOM's ingest path never reads are touched. The driver name and `semanticVersion`
stay, because the ingest path refuses SARIF produced by a different CodeQL.
"""
import json
import os
import sys

PLACEHOLDER = "file:///codeql/"

# Fixtures live in this repository and nowhere else, so that is the whole of what this may rewrite.
ROOT = os.path.realpath(os.path.join(os.path.dirname(os.path.realpath(__file__)), ".."))


def inside_repo(path):
    """Resolves a path and refuses one that leaves the repository.

    The argument comes from the command line, and this rewrites whatever it names in place. Links
    are followed before the comparison, so a symlink inside the tree cannot point out of it, and
    the separator is part of the prefix because `/repo-elsewhere` starts with `/repo`.
    """
    resolved = os.path.realpath(path)
    if not resolved.startswith(ROOT + os.sep):
        # Both repr'd: ROOT is a filesystem path too, decided by where the checkout was placed,
        # so a newline in it would forge a line of output just as readily as one in the argument.
        raise SystemExit(f"refusing to rewrite {path!r}: outside {ROOT!r}")
    return resolved


def scrub_uri(value):
    """Keeps the tail of a pack path so a reader can still tell which pack it was."""
    if not isinstance(value, str) or not value.startswith("file://"):
        return value
    for marker in ("/packages/", "/ql/"):
        at = value.find(marker)
        if at != -1:
            return PLACEHOLDER + value[at + 1:]
    return PLACEHOLDER


def walk(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "uri":
                node[key] = scrub_uri(value)
            else:
                walk(value)
    elif isinstance(node, list):
        for item in node:
            walk(item)


def main(paths):
    for path in paths:
        fixture = inside_repo(path)
        with open(fixture) as handle:
            doc = json.load(handle)
        for run in doc.get("runs", []):
            # Wall-clock times and the command line that produced them: different on every run,
            # and read by nothing.
            run.pop("invocations", None)
            walk(run.get("tool", {}))
        with open(fixture, "w") as handle:
            json.dump(doc, handle, indent=1, sort_keys=True)
            handle.write("\n")


if __name__ == "__main__":
    main(sys.argv[1:])
