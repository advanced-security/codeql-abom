package authoring

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Printable is a query's one test database and the fixtures in it, which is what a tree is printed
// from. Why says what stands in the way, and is empty when nothing does.
func Printable(root string, q Query) (database string, fixtures []string, why string, err error) {
	dirs := testDirs(q.Tests)
	switch len(dirs) {
	case 0:
		return "", nil, "no test names " + q.Rel + ", so there is no test database to print", nil
	case 1:
	default:
		return "", nil, fmt.Sprintf("%s is tested in %d directories, and a tree is printed from one", q.Rel,
			len(dirs)), nil
	}
	dir := dirs[0]
	database, why, err = printableDatabase(root, dir)
	if why != "" || err != nil {
		return "", nil, why, err
	}
	fixtures, err = printableFixtures(dir)
	if err != nil {
		return "", nil, "", err
	}
	if len(fixtures) == 0 {
		return "", nil, relTo(root, dir) + " holds no workflow or action to print", nil
	}
	slices.Sort(fixtures)
	return database, fixtures, "", nil
}

func testDirs(tests []string) []string {
	dirs := []string{}
	for _, t := range tests {
		if dir := filepath.Dir(t); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func printableDatabase(root, dir string) (database, why string, err error) {
	database = filepath.Join(dir, filepath.Base(dir)+".testproj")
	yml := filepath.Join(database, "codeql-database.yml")
	info, err := os.Stat(yml)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", relTo(root, database) + " has not been extracted; run the query's tests first", nil
	case err != nil:
		return "", "", err
	}
	finished, err := extractionFinished(yml)
	if err != nil {
		return "", "", err
	}
	if !finished {
		return "", relTo(root, database) + " did not finish extracting; run the query's tests again", nil
	}
	newest, err := newestSource(dir)
	if err != nil {
		return "", "", err
	}
	if newest.After(info.ModTime()) {
		return "", relTo(root, database) + " is older than its fixtures, so a tree would describe code " +
			"that is gone; run the query's tests again", nil
	}
	return database, "", nil
}

func printableFixtures(dir string) ([]string, error) {
	fixtures := []string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && strings.HasSuffix(d.Name(), ".testproj"):
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		if ext := filepath.Ext(p); ext == ".yml" || ext == ".yaml" {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			fixtures = append(fixtures, filepath.ToSlash(rel))
		}
		return nil
	})
	return fixtures, err
}
