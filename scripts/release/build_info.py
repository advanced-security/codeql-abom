#!/usr/bin/env python3
"""Writes and verifies a release archive's build-info.json, run from the archive's root.

    build_info.py write --version V [--tools JSON] [--cc TEXT]
    build_info.py verify

`write` reads the executable's own identity, so the manifest agrees with it by construction, then
adds what packaging knows: the packs, the tools that built it, what the binary requires of the
platform and a hash of every other file. `verify` recomputes all of that from an extracted archive
and names each disagreement, so the bytes tested are shown to be the bytes described.

The archive is the working directory rather than an argument, so no path this reads or writes and
no command it runs is chosen by whoever invokes it.
"""

import argparse
import hashlib
import json
import platform
import re
import subprocess
import sys
from pathlib import Path

SCHEMA_VERSION = 1
ROOT = Path(".")
MANIFEST = "build-info.json"
EXECUTABLE = "codeql-abom"
ASSETS = {"ui": "ui", "packs": "ql", "workspace": "codeql-workspace.yml"}
TARGETS = {("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"), ("darwin", "arm64")}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def inventory(root: Path) -> list[dict]:
    files = []
    for path in sorted(p for p in root.rglob("*") if p.is_file() or p.is_symlink()):
        relative = path.relative_to(root).as_posix()
        if relative == MANIFEST:
            continue
        if path.is_symlink():
            raise SystemExit(f"build-info: {relative} is a symbolic link, which an archive must not carry")
        files.append({"path": relative, "size": path.stat().st_size, "sha256": sha256(path)})
    return files


def identity() -> dict:
    # A bare name would be looked up on PATH rather than taken from the archive.
    out = subprocess.run(["./" + EXECUTABLE, "version", "--json"],
                         check=True, capture_output=True, text=True).stdout
    return json.loads(out)


def scalar(text: str, key: str) -> str:
    match = re.search(rf"^{re.escape(key)}:\s*(\S+)", text, re.MULTILINE)
    return match.group(1).strip("'\"") if match else ""


def block(text: str, key: str) -> dict[str, str]:
    """A top-level mapping of `name: value` lines, as manifests and lock files write them."""
    out, inside, current = {}, False, ""
    for line in text.splitlines():
        if re.match(rf"^{re.escape(key)}:\s*$", line):
            inside = True
            continue
        if inside and line and not line.startswith(" "):
            break
        if not inside or not line.strip():
            continue
        entry = re.match(r"^  (\S+):\s*(\S*)", line)
        nested = re.match(r"^    version:\s*(\S+)", line)
        if entry:
            current = entry.group(1).strip("'\"")
            out[current] = entry.group(2).strip("'\"")
        elif nested and current:
            out[current] = nested.group(1).strip("'\"")
    return out


def pack_digest(folder: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(p for p in folder.rglob("*") if p.is_file()):
        digest.update(path.relative_to(folder).as_posix().encode() + b"\0")
        digest.update(sha256(path).encode() + b"\n")
    return digest.hexdigest()


def packs(root: Path) -> list[dict]:
    out = []
    for manifest in sorted((root / ASSETS["packs"]).rglob("codeql-pack.yml")):
        folder = manifest.parent
        text = manifest.read_text(encoding="utf-8")
        lock = folder / "codeql-pack.lock.yml"
        out.append({
            "name": scalar(text, "name"),
            "version": scalar(text, "version"),
            "path": folder.relative_to(root).as_posix(),
            "sha256": pack_digest(folder),
            "dependencies": block(text, "dependencies"),
            "locked": block(lock.read_text(encoding="utf-8"), "dependencies") if lock.exists() else {},
        })
    return out


def command_line(*argv: str) -> str:
    try:
        out = subprocess.run(list(argv), check=True, capture_output=True, text=True).stdout
    except (OSError, subprocess.CalledProcessError):
        return ""
    return out.strip().splitlines()[0] if out.strip() else ""


def platform_requirements(executable: Path, target_os: str) -> dict:
    """What the executable demands of the platform, measured from the binary rather than assumed."""
    if target_os == "linux":
        symbols = subprocess.run(["objdump", "-T", str(executable)],
                                 check=True, capture_output=True, text=True).stdout
        versions = {tuple(int(n) for n in v.split(".")) for v in re.findall(r"GLIBC_([0-9.]+)", symbols)}
        release = dict(line.split("=", 1) for line in Path("/etc/os-release").read_text().splitlines()
                       if "=" in line)
        return {
            "libc": "glibc",
            "minimumSymbolVersion": ".".join(map(str, max(versions))) if versions else "",
            "builtOn": release.get("PRETTY_NAME", "").strip('"'),
        }
    loads = subprocess.run(["otool", "-l", str(executable)],
                           check=True, capture_output=True, text=True).stdout
    minos = re.search(r"cmd LC_BUILD_VERSION.*?minos (\S+)", loads, re.DOTALL)
    return {
        "minimumOS": minos.group(1) if minos else "",
        "builtOn": f"macOS {platform.mac_ver()[0]}",
    }


def write(args: argparse.Namespace) -> int:
    root = ROOT
    ident = identity()
    semver = args.version.removeprefix("v")
    problems = []
    if ident["development"] or ident["version"] != semver:
        problems.append(f"the executable says {ident['version'] or 'development build'}, not {semver}")
    if ident["dirty"] is not False:
        problems.append("the executable was not built from a clean, recorded tree")
    if (ident["os"], ident["arch"]) not in TARGETS:
        problems.append(f"{ident['os']}/{ident['arch']} is not a release target")
    if problems:
        print("build-info: " + "; ".join(problems), file=sys.stderr)
        return 1

    tools = {"go": ident["goVersion"], "cc": args.cc or command_line("cc", "--version")}
    if args.tools:
        tools.update(json.loads(args.tools))

    info = {
        "schemaVersion": SCHEMA_VERSION,
        "version": semver,
        "revision": ident["revision"],
        "dirty": ident["dirty"],
        "target": {"os": ident["os"], "arch": ident["arch"]},
        "codeql": {"version": ident["codeqlVersion"]},
        "assets": ASSETS,
        "packs": packs(root),
        "buildTools": tools,
        "platformRequirements": platform_requirements(root / EXECUTABLE, ident["os"]),
        "files": inventory(root),
    }
    (root / MANIFEST).write_text(json.dumps(info, indent=2) + "\n", encoding="utf-8")
    return 0


def verify(_: argparse.Namespace) -> int:
    root = ROOT
    info = json.loads((root / MANIFEST).read_text(encoding="utf-8"))
    if info.get("schemaVersion") != SCHEMA_VERSION:
        print(f"build-info: schema version {info.get('schemaVersion')} is not one this can read",
              file=sys.stderr)
        return 1

    problems = []
    ident = identity()
    expected = {
        "version": info["version"], "revision": info["revision"], "dirty": info["dirty"],
        "os": info["target"]["os"], "arch": info["target"]["arch"],
        "codeqlVersion": info["codeql"]["version"],
    }
    for key, want in expected.items():
        if ident.get(key) != want:
            problems.append(f"the executable's {key} is {ident.get(key)!r}, the manifest says {want!r}")
    if ident.get("development") or info["dirty"] is not False:
        problems.append("a development or modified build is not a release")

    recorded = {f["path"]: f for f in info["files"]}
    actual = {f["path"]: f for f in inventory(root)}
    for path in sorted(recorded.keys() - actual.keys()):
        problems.append(f"{path} is listed but missing")
    for path in sorted(actual.keys() - recorded.keys()):
        problems.append(f"{path} is present but not listed")
    for path in sorted(recorded.keys() & actual.keys()):
        if recorded[path] != actual[path]:
            problems.append(f"{path} does not match its recorded size and hash")
    for pack in packs(root):
        listed = next((p for p in info["packs"] if p["path"] == pack["path"]), None)
        if listed != pack:
            problems.append(f"pack {pack['name']} at {pack['path']} does not match the manifest")

    for problem in problems:
        print(f"build-info: {problem}", file=sys.stderr)
    if problems:
        return 1
    print(f"build-info: {Path.cwd().name} matches its manifest ({len(recorded)} files)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    w = sub.add_parser("write")
    w.add_argument("--version", required=True)
    w.add_argument("--tools", help="JSON object of further tools that built the archive")
    w.add_argument("--cc", help="the C compiler's version line, when `cc --version` is not it")
    sub.add_parser("verify")
    args = parser.parse_args()
    return write(args) if args.command == "write" else verify(args)


if __name__ == "__main__":
    sys.exit(main())
