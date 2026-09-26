// Package authoring reads the operator's query workspace as it is on disk now: the queries it
// holds, the tests that name each one, and the identity of those tests.
package authoring

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/runner"
)

// Query is one query in the workspace.
type Query struct {
	ID   string
	Kind string
	// Path is the query file; Rel is the same file relative to the pack, which is how a .qlref
	// names it.
	Path    string
	Rel     string
	Version string
	// Tests are the .qlref files naming this query. Empty means nothing tests it.
	Tests []string
}

// Workspace lists every query under root/src with the tests that name it.
//
// Read from the files rather than from `codeql resolve`: the CLI takes the machine-wide lock, and
// a listing asked for on every page load must not queue behind a crawl.
func Workspace(root string) ([]Query, error) {
	tests, err := testsByQuery(root)
	if err != nil {
		return nil, err
	}
	out := []Query{}
	src := filepath.Join(root, "src")
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != src && strings.HasPrefix(d.Name(), "."):
			return filepath.SkipDir
		case d.IsDir() || filepath.Ext(p) != ".ql":
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		q := Query{Path: p, Rel: filepath.ToSlash(rel), Tests: []string{}}
		if q.ID, q.Kind, err = metadata(p); err != nil {
			return err
		}
		q.Tests = append(q.Tests, tests[q.Rel]...)
		out = append(out, q)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("listing the queries in %s: %w", root, err)
	}

	paths := make([]string, 0, len(out))
	for _, q := range out {
		paths = append(paths, q.Path)
	}
	versions, err := runner.QueryVersions(paths)
	if err != nil {
		return nil, fmt.Errorf("versioning the queries in %s: %w", root, err)
	}
	for i := range out {
		out[i].Version = versions[i]
	}
	return out, nil
}

// Find is the workspace query that declares id.
func Find(root, id string) (Query, bool, error) {
	queries, err := Workspace(root)
	if err != nil {
		return Query{}, false, err
	}
	var found []Query
	for _, q := range queries {
		if q.ID == id {
			found = append(found, q)
		}
	}
	switch len(found) {
	case 0:
		return Query{}, false, nil
	case 1:
		return found[0], true, nil
	}
	// Refused rather than picked: nothing would say which of the two a result was about.
	return Query{}, false, fmt.Errorf("%s is declared by both %s and %s", id, found[0].Rel,
		found[1].Rel)
}

// testsByQuery maps a query's pack-relative path to the .qlref files naming it.
func testsByQuery(root string) (map[string][]string, error) {
	out := map[string][]string{}
	dir := filepath.Join(root, "test")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != dir &&
			(strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".testproj")):
			return filepath.SkipDir
		case d.IsDir() || filepath.Ext(p) != ".qlref":
			return nil
		}
		named, err := qlrefTarget(p)
		if err != nil {
			return err
		}
		out[named] = append(out[named], p)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("listing the tests in %s: %w", dir, err)
	}
	return out, nil
}

// qlrefTarget is the query a .qlref names: a bare path, or YAML with a `query:` key.
func qlrefTarget(file string) (string, error) {
	body, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(body))
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "query:"); ok {
			text = strings.Trim(strings.TrimSpace(value), `"'`)
			break
		}
	}
	return path.Clean(filepath.ToSlash(text)), nil
}

// metadata reads @id and @kind from a query's leading QLDoc comment, which is where the CLI
// reads them from.
func metadata(file string) (id, kind string, err error) {
	f, err := os.Open(file)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inside := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !inside {
			rest, ok := strings.CutPrefix(line, "/**")
			if !ok {
				continue
			}
			inside, line = true, rest
		}
		body, closed := strings.CutSuffix(line, "*/")
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(body), "*"))
		if len(fields) >= 2 {
			switch fields[0] {
			case "@id":
				id = fields[1]
			case "@kind":
				kind = fields[1]
			}
		}
		if closed {
			break
		}
	}
	return id, kind, scanner.Err()
}

// DigestTests identifies the tests as they are now: every file in each test's directory, keyed
// by its path in the workspace.
//
// A run's own leavings are not part of what a test says, so `.actual` output and `.testproj`
// databases are left out. Empty when there are no tests, which is not a digest of nothing.
func DigestTests(root string, tests []string) (string, error) {
	return digestFiles(root, tests, func(p string) bool { return filepath.Ext(p) != ".actual" })
}

// DigestFixtures identifies the code a test database is built from: the tests' files less those
// the CLI reads after extraction, so accepting a report leaves it as it was.
func DigestFixtures(root string, tests []string) (string, error) {
	return digestFiles(root, tests, func(p string) bool {
		switch filepath.Ext(p) {
		case ".actual", ".expected", ".qlref", ".ql":
			return false
		}
		return true
	})
}

func digestFiles(root string, tests []string, keep func(string) bool) (string, error) {
	if len(tests) == 0 {
		return "", nil
	}
	files := map[string]string{}
	for _, test := range tests {
		dir := filepath.Dir(test)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir() && strings.HasSuffix(d.Name(), ".testproj"):
				return filepath.SkipDir
			case d.IsDir() || !keep(p):
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(body)
			files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("reading the tests in %s: %w", dir, err)
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		// Length-prefixed, so no two names and bodies can concatenate to a third that hashes the
		// same.
		fmt.Fprintf(h, "%d:%s\x00%s\x00", len(name), name, files[name])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
