package runner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// customRoot is where an operator's own CodeQL pack lives, when they have one.
//
// A package-level value rather than a field on Runner: `localPackArgs` is reached from the three
// places that shell out to the CLI, and threading a search path through each of them would let
// one of them resolve a query the other two cannot see — which reads as a query that compiles and
// then vanishes.
var customRoot struct {
	sync.RWMutex
	dir string
}

// SetCustomPackRoot names a directory to search for packs besides this repository's own.
//
// Empty turns it off. The CLI refuses a pack name found in two places on one search path, so a
// custom pack must not reuse a name under `ql/`; `customql.Scaffold` owns that.
func SetCustomPackRoot(dir string) {
	customRoot.Lock()
	defer customRoot.Unlock()
	customRoot.dir = dir
}

// CustomPackRoot reports the operator's pack directory, or empty when none is configured.
func CustomPackRoot() string {
	customRoot.RLock()
	defer customRoot.RUnlock()
	return customRoot.dir
}

// packSearchPath lists the directories the CLI should search, skipping any that is not there.
//
// A configured directory that does not exist is skipped rather than refused: it is created when
// the operator first writes a query, and refusing every CLI call until then would make naming the
// directory an act that breaks the crawl.
func packSearchPath() []string {
	out := []string{}
	for _, dir := range []string{PackRoot, CustomPackRoot()} {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		out = append(out, abs)
	}
	return out
}

// PackSearchPath is where this process's CLI invocations resolve packs, for a tool that runs the
// CLI itself and has to resolve them the same way.
func PackSearchPath() []string { return packSearchPath() }

// BindableSuites lists every suite a tier may be bound to, across this repository's packs and the
// operator's.
//
// Both roots, because a query an operator deployed has to be offerable in the same grid as the
// shipped ones; a control that cannot see it makes deploying a stage with nowhere to go.
func BindableSuites() ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, dir := range packSearchPath() {
		suites, err := AvailableSuites(dir)
		if err != nil {
			return nil, err
		}
		for _, suite := range suites {
			if seen[suite] {
				continue
			}
			seen[suite] = true
			out = append(out, suite)
		}
	}
	sort.Strings(out)
	return out, nil
}

// localPackArgs lets the CLI resolve this repository's packs, and the operator's, by name.
//
// A `<pack>:<suite>` reference is looked up in the package cache, which a pack that has never
// been published is not in. Without this the CLI reports it as misspelled rather than as local.
// Omitted when no directory is there, so a build running against installed packs alone is not
// handed a search path that does not exist.
func localPackArgs() []string {
	dirs := packSearchPath()
	if len(dirs) == 0 {
		return nil
	}
	// The CLI takes `<dir>[:<dir>...]`, and it is an error for one pack name to be found twice on
	// the list — which is why a custom pack is named apart from everything under `ql/`.
	return []string{"--additional-packs", strings.Join(dirs, string(os.PathListSeparator))}
}
