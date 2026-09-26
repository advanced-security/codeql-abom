package runner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackRoot is where this repository's CodeQL packs live, relative to the working directory.
//
// A path rather than a compiled-in list, because the query set is meant to grow: adding a suite
// is dropping a `.qls` into a pack, and it has to be offered without a release.
const PackRoot = "ql"

// manifestNames are the two spellings a CodeQL pack manifest takes.
var manifestNames = map[string]bool{"codeql-pack.yml": true, "qlpack.yml": true}

// AvailableSuites lists every suite the packs under root provide, as `<pack>:<path>` references,
// sorted.
//
// Pack-qualified rather than a file path because that is the identity that survives both moving
// the directory and publishing the pack, and it is the form the CodeQL CLI already accepts.
//
// A missing root offers nothing rather than failing: a binding may legitimately name suites from
// an installed pack instead, and a build without ABOM's own is not broken.
func AvailableSuites(root string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Both spellings, as `PackOf` reads: this repository writes codeql-pack.yml, and a pack
		// an operator could publish writes qlpack.yml. Reading only one makes a deployed query
		// resolvable by the CLI and invisible to the control that would bind it.
		if d.IsDir() || !manifestNames[d.Name()] {
			return nil
		}
		suites, err := suitesOfPack(path)
		if err != nil {
			return err
		}
		out = append(out, suites...)
		return nil
	})
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading packs under %s: %w", root, err)
	}
	// Sorted so a control built from this does not reshuffle between reads.
	sort.Strings(out)
	return out, nil
}

// ciOnlyPacks hold suites that gate this repository against its own workflows rather than
// analyzing a corpus. Offering one as a binding advertises a control that answers no question the
// corpus asks.
//
// A pack rather than a suite name, which is what makes this affordable: a suite dropped into a
// pack that is for analysis still appears with no release, and only a pack that exists solely for
// CI has to be named. `make selfcheck` passes the suite directly, so nothing that needs it comes
// through here.
var ciOnlyPacks = map[string]bool{
	"advanced-security/abom-actions-selfcheck-src": true,
}

// devOnlySuites hold queries an operator is still working on. A tier bound to one would run the
// whole custom pack across the corpus the moment a file was saved, which makes writing a query an
// expensive accident. The deployed suite beside it is what a tier may bind.
var devOnlySuites = map[string]bool{
	"codeql-suites/abom-custom.qls": true,
}

// suitesOfPack reads one manifest and lists the suites it declares.
func suitesOfPack(manifest string) ([]string, error) {
	name, _, suiteDir, err := packIdentity(manifest)
	if err != nil {
		return nil, err
	}
	// A pack that declares no suite directory has none to offer, which is not an error.
	if name == "" || suiteDir == "" || ciOnlyPacks[name] {
		return nil, nil
	}
	if !filepath.IsLocal(suiteDir) {
		return nil, fmt.Errorf("suite directory %q must stay inside pack %s", suiteDir, name)
	}

	dir, err := os.OpenInRoot(filepath.Dir(manifest), suiteDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening suites in pack %s: %w", name, err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("reading suites in pack %s: %w", name, err)
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".qls") {
			continue
		}
		// Forward slashes always: a suite reference stops being a filesystem path once it names
		// a pack, and the same string has to work on every platform.
		ref := suiteDir + "/" + e.Name()
		if devOnlySuites[ref] {
			continue
		}
		out = append(out, name+":"+ref)
	}
	return out, nil
}

// PackOf attributes a resolved query to the pack that supplies it, and names the directory the
// pack lives in so its contents can be hashed.
//
// Found by walking up to the nearest manifest rather than by asking the CLI, so provenance for a
// whole suite costs a few file reads instead of a process per query.
func PackOf(queryPath string) (name, version, dir string) {
	at := filepath.Dir(queryPath)
	for {
		// Both spellings: this repository writes codeql-pack.yml, and the registry writes
		// qlpack.yml into the package cache, which is where most attributed queries live.
		for _, manifest := range []string{"codeql-pack.yml", "qlpack.yml"} {
			path := filepath.Join(at, manifest)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			n, v, _, err := packIdentity(path)
			if err != nil {
				return "", "", ""
			}
			return n, v, at
		}
		parent := filepath.Dir(at)
		// A query outside any pack has no provenance to report, which is a fact rather than an
		// error: the caller records what it knows and leaves the rest blank.
		if parent == at {
			return "", "", ""
		}
		at = parent
	}
}

// packIdentity reads the manifest fields a suite reference and its provenance need.
//
// Scanned rather than parsed as YAML: pulling in a parser to read three scalars would be the only
// reason this module needed one.
func packIdentity(manifest string) (name, version, suiteDir string, err error) {
	body, err := os.ReadFile(manifest)
	if err != nil {
		return "", "", "", fmt.Errorf("reading %s: %w", manifest, err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// Only top-level keys: an indented `name:` belongs to a dependency, not to this pack.
		if key != strings.TrimLeft(key, " \t") {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "version":
			version = value
		case "suites":
			suiteDir = value
		}
	}
	return name, version, suiteDir, nil
}
