package execution

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/advanced-security/codeql-abom/internal/runner"
)

// PrivateTests runs a session's tests on a copy of the pack at workspace, in dir, which only this
// process writes, one run at a time: run is handed the copy and the tests within it.
//
// The agent beside a session's worker shares the pack, and the CLI follows a link in a test's code,
// copying whatever it names into the database it builds beside the test, where the agent reads it.
// The copy holds the pack's regular files, less what a run leaves; a link or a pipe anywhere else in
// the pack refuses the run. What the run leaves beside each test goes back beside it in the pack as
// the run left it, through no link, and what the run says names the pack rather than the copy.
func PrivateTests(workspace, dir string,
	run func(ctx context.Context, pack string, tests []string) (runner.TestResult, error)) func(context.Context, []string) (runner.TestResult, error) {
	var mu sync.Mutex
	return func(ctx context.Context, tests []string) (runner.TestResult, error) {
		mu.Lock()
		defer mu.Unlock()
		rels := make([]string, 0, len(tests))
		for _, t := range tests {
			rel, err := filepath.Rel(workspace, t)
			if err != nil || !filepath.IsLocal(rel) {
				return runner.TestResult{}, fmt.Errorf("%s is not in the pack", t)
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		if err := os.RemoveAll(dir); err != nil {
			return runner.TestResult{}, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		if err := copyRegular(workspace, dir); err != nil {
			return runner.TestResult{}, err
		}
		named := naming(dir, workspace)
		inCopy := make([]string, len(rels))
		for i, rel := range rels {
			inCopy[i] = filepath.Join(dir, filepath.FromSlash(rel))
		}
		result, err := run(ctx, dir, inCopy)
		if err != nil {
			return runner.TestResult{}, renamed{err: err, named: named}
		}
		result.Detail = named(result.Detail)
		if err := putBack(dir, workspace, leftIn(dir, rels)); err != nil {
			return runner.TestResult{}, fmt.Errorf("putting back what the tests left: %w", err)
		}
		return result, nil
	}
}

// copyRegular copies the pack at workspace to dir: each regular file, read through no link, less what
// a run leaves and the caches, within the pack's bound. Anything else in the pack refuses the copy.
func copyRegular(workspace, dir string) error {
	pack, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = pack.Close() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	var size int64
	return fs.WalkDir(pack.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
			return nil
		case leaving(d) && d.IsDir():
			return fs.SkipDir
		case d.IsDir():
			return out.Mkdir(filepath.FromSlash(p), 0o755)
		case leaving(d):
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file, and a test run reads nothing else", p)
		}
		body, err := readIn(pack, p, packLimit-size)
		if err != nil {
			return err
		}
		size += int64(len(body))
		return out.WriteFile(filepath.FromSlash(p), body, 0o644)
	})
}

// putBack puts what a run in the copy at from left in each of dirs, slash paths, back in the same
// directory of the pack at to: each .actual and each database, in place of what an earlier run left
// there, dated as the run dated it, which is what says whether a database is older than its test.
// Each is created anew, so nothing the agent plants in its place is followed or written through.
func putBack(from, to string, dirs []string) error {
	src, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenRoot(to)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	for _, d := range dirs {
		earlier, err := fs.ReadDir(dst.FS(), d)
		if err != nil {
			return err
		}
		for _, e := range earlier {
			if strings.HasSuffix(e.Name(), ".actual") || strings.HasSuffix(e.Name(), ".testproj") {
				if err := dst.RemoveAll(filepath.FromSlash(path.Join(d, e.Name()))); err != nil {
					return err
				}
			}
		}
		left, err := fs.ReadDir(src.FS(), d)
		if err != nil {
			return err
		}
		for _, e := range left {
			name := path.Join(d, e.Name())
			switch {
			case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".actual"):
				err = putOne(src, dst, name, e)
			case e.IsDir() && strings.HasSuffix(e.Name(), ".testproj"):
				err = putDatabase(src, dst, name)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// putDatabase puts the database at name in src back at name in dst, within what a test database may
// hold.
func putDatabase(src, dst *os.Root, name string) error {
	var size int64
	return fs.WalkDir(src.FS(), name, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return dst.Mkdir(filepath.FromSlash(p), 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if size += info.Size(); size > databaseLimit {
			return fmt.Errorf("%s holds more than %d MB", name, databaseLimit>>20)
		}
		return putOne(src, dst, p, d)
	})
}

// putOne writes the regular file at name in src as a new file at name in dst, dated as it is in src.
func putOne(src, dst *os.Root, name string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	in, err := src.Open(filepath.FromSlash(name))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := dst.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err := errors.Join(err, out.Close()); err != nil {
		return err
	}
	return dst.Chtimes(filepath.FromSlash(name), info.ModTime(), info.ModTime())
}

// naming names the pack at pack wherever s names the copy at copied, as given or with its links
// resolved, which is how the CLI names it.
func naming(copied, pack string) func(s string) string {
	forms := []string{copied}
	if real, err := filepath.EvalSymlinks(copied); err == nil && real != copied {
		forms = append(forms, real)
	}
	// Longest first: the resolved form can hold the other, as /private/var holds /var.
	slices.SortFunc(forms, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return func(s string) string {
		for _, f := range forms {
			s = strings.ReplaceAll(s, f, pack)
		}
		return s
	}
}

// renamed is an error from a run in the copy, saying what it says with the copy named as the pack.
type renamed struct {
	err   error
	named func(string) string
}

func (r renamed) Error() string { return r.named(r.err.Error()) }
func (r renamed) Unwrap() error { return r.err }
