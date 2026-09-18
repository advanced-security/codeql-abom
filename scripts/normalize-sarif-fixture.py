#!/usr/bin/env python3
"""Strips machine-specific and volatile data out of a CodeQL SARIF fixture, in place.

A fixture is committed, so anything in it that differs between two machines shows up as a diff
nobody made: the CLI records the absolute path of every pack it resolved, which is a home
directory and a checkout layout, and it stamps each invocation with a wall-clock time.

Only fields ABOM's ingest path never reads are touched. The driver name and `semanticVersion`
stay, because the ingest path refuses SARIF produced by a different CodeQL.
"""
import json
import sys

PLACEHOLDER = "file:///codeql/"


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
        with open(path) as handle:
            doc = json.load(handle)
        for run in doc.get("runs", []):
            # Wall-clock times and the command line that produced them: different on every run,
            # and read by nothing.
            run.pop("invocations", None)
            walk(run.get("tool", {}))
        with open(path, "w") as handle:
            json.dump(doc, handle, indent=1, sort_keys=True)
            handle.write("\n")


if __name__ == "__main__":
    main(sys.argv[1:])
