#!/usr/bin/env python3
"""Writes the third-party notices for what a release archive ships.

    notices.py go <executable>   modules linked into the executable
    notices.py nuget             packages the published interface was restored from
    notices.py nuget-cyclonedx   the same packages as CycloneDX, which the release SBOM reads
    notices.py web               scripts vendored into the interface, under ui/wwwroot/js

Go modules are read from the executable itself, so a module that is only a test dependency is not
listed and one that is linked in cannot be missed. A component whose license text cannot be found
fails the run: shipping without it is the one outcome this file exists to prevent. The interface
needs a CycloneDX list as well because it ships as WebAssembly, in which Syft finds no packages.
No package manager restored the vendored scripts, so their notices are kept beside them, and each
`*.min.js` there must have a section with its license text.
"""

import json
import re
import subprocess
import sys
from pathlib import Path

LICENSE_FILE = re.compile(
    r"^(licen[cs]e|copying|notice|patents|third-party-notices)([-._].*)?$", re.IGNORECASE)

# Where `dotnet publish` of the interface leaves its restore, fixed rather than passed in.
ASSETS = Path(__file__).resolve().parents[2] / "ui" / "obj" / "project.assets.json"

# Fixed for the same reason; tests point it elsewhere.
VENDORED = Path(__file__).resolve().parents[2] / "ui" / "wwwroot" / "js"
VENDORED_NOTICES = "THIRD_PARTY_NOTICES.txt"
SECTION = re.compile(r"^={78}\n([^\n]+)\n-{78}\n(.*?)(?=^={78}$|\Z)", re.MULTILINE | re.DOTALL)


def license_texts(folder: Path) -> list[tuple[str, str]]:
    """Every license-like file at the root of a module or package, sorted by name."""
    found = []
    for path in sorted(folder.iterdir()):
        if path.is_file() and LICENSE_FILE.match(path.name):
            found.append((path.name, path.read_text(encoding="utf-8", errors="replace")))
    return found


def section(name: str, version: str, body: str) -> str:
    rule = "=" * 78
    return f"{rule}\n{name} {version}\n{'-' * 78}\n{body.rstrip()}\n\n"


def linked_modules(executable: str) -> list[tuple[str, str]]:
    out = subprocess.run(["go", "version", "-m", executable],
                         check=True, capture_output=True, text=True).stdout
    modules = []
    for line in out.splitlines():
        fields = line.strip().split("\t")
        if fields[0] == "dep" and len(fields) >= 3:
            modules.append((fields[1], fields[2]))
        elif fields[0] == "=>" and len(fields) >= 3 and modules:
            # A replacement is what was actually linked, so its license is the one that applies.
            modules[-1] = (fields[1], fields[2])
    return modules


def go_notices(executable: str) -> tuple[str, list[str]]:
    text, missing = [], []
    for path, version in linked_modules(executable):
        info = json.loads(subprocess.run(["go", "mod", "download", "-json", f"{path}@{version}"],
                                         check=True, capture_output=True, text=True).stdout)
        texts = license_texts(Path(info["Dir"]))
        if not texts:
            missing.append(f"{path}@{version}")
            continue
        text.append(section(path, version, "\n\n".join(body for _, body in texts)))
    return "".join(text), missing


def nuspec_license(folder: Path) -> str:
    """The license a package declares in its manifest, when it ships no license file."""
    for nuspec in folder.glob("*.nuspec"):
        manifest = nuspec.read_text(encoding="utf-8", errors="replace")
        expression = re.search(r'<license[^>]*type="expression"[^>]*>([^<]+)</license>', manifest)
        if expression:
            return f"License: {expression.group(1).strip()} (SPDX expression; the package ships no text)"
        url = re.search(r"<licenseUrl>([^<]+)</licenseUrl>", manifest)
        if url:
            return f"License: see {url.group(1).strip()} (the package ships no text)"
    return ""


def nuget_packages() -> tuple[list[Path], list[tuple[str, str, str]]]:
    """The package folders, and each (name, version, path) the interface was restored from."""
    assets = json.loads(ASSETS.read_text(encoding="utf-8"))
    roots = [Path(p) for p in assets.get("packageFolders", {})]
    # Keyed without case, as NuGet compares ids; the first spelling seen, the restore's own, is kept.
    packages = {}
    for key, library in assets.get("libraries", {}).items():
        if library.get("type") == "package":
            name, version = key.split("/", 1)
            packages.setdefault((name.lower(), version), (name, version, library["path"]))
    # The browser runtime is downloaded rather than referenced, and it is what ships in _framework.
    for framework in assets.get("project", {}).get("frameworks", {}).values():
        for dep in framework.get("downloadDependencies", []):
            version = dep["version"].strip("[]").split(",")[0].strip()
            packages.setdefault((dep["name"].lower(), version),
                                (dep["name"], version, f"{dep['name'].lower()}/{version}"))
    return roots, [packages[key] for key in sorted(packages)]


def nuget_notices() -> tuple[str, list[str]]:
    roots, packages = nuget_packages()
    text, missing = [], []
    for name, version, relative in packages:
        folder = next((root / relative for root in roots if (root / relative).is_dir()), None)
        if folder is None:
            missing.append(f"{name}@{version} (not restored)")
            continue
        texts = license_texts(folder)
        body = "\n\n".join(t for _, t in texts) or nuspec_license(folder)
        if not body:
            missing.append(f"{name}@{version}")
            continue
        text.append(section(name, version, body))
    return "".join(text), missing


def nuget_cyclonedx() -> str:
    """The interface's packages as a CycloneDX document, with nothing in it that varies by run."""
    _, packages = nuget_packages()
    components = []
    for name, version, _ in packages:
        purl = f"pkg:nuget/{name}@{version}"
        components.append({"type": "library", "bom-ref": purl, "name": name,
                           "version": version, "purl": purl})
    bom = {"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
           "components": components}
    return json.dumps(bom, indent=2) + "\n"


def web_notices(folder: Path) -> tuple[str, list[str]]:
    """The kept notices, and each vendored script that no section with license text covers."""
    notices = folder / VENDORED_NOTICES
    text = notices.read_text(encoding="utf-8") if notices.is_file() else ""
    # A section is headed "<script>: <package> <version>"; one with nothing under it names a license
    # without the text that license requires.
    covered = {heading.split(": ", 1)[0] for heading, body in SECTION.findall(text) if body.strip()}
    missing = [script.name for script in sorted(folder.glob("*.min.js")) if script.name not in covered]
    return text, missing


def main(argv: list[str]) -> int:
    if argv[1:2] == ["go"] and len(argv) == 3:
        text, missing = go_notices(argv[2])
    elif argv[1:] == ["nuget"]:
        text, missing = nuget_notices()
    elif argv[1:] == ["web"]:
        text, missing = web_notices(VENDORED)
    elif argv[1:] == ["nuget-cyclonedx"]:
        sys.stdout.write(nuget_cyclonedx())
        return 0
    else:
        print(__doc__, file=sys.stderr)
        return 2
    if missing:
        print("notices: no license found for " + ", ".join(missing), file=sys.stderr)
        return 1
    sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
