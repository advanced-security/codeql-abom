package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/regular"
)

// Change is a file a session left different from how it read it, by slash path relative to the
// pack: the digest of what it read, empty for one it created, and of what it left, empty for one it
// removed, with what it left.
type Change struct {
	Path          string
	Before, After string
	Body          []byte
}

// Land writes what a session a worker ran changed into the pack at workspace, all of it or none, and
// answers the session's test directory, a slash path relative to the pack, beside which what its test
// runs left belongs. Every change must replace exactly the file the session read, and be one the
// session's bounds allowed once bound to target, as locate binds a session, in the pack as it would
// be once landed: a worker's identity can sign any change, so the gate's rule is applied again here.
func Land(workspace, target string, changes []Change) (string, error) {
	if target == "" {
		if len(changes) > 0 {
			return "", fmt.Errorf("the session changed %s without deciding the query its request is about",
				changes[0].Path)
		}
		return "", nil
	}
	for _, c := range changes {
		if err := c.holds(workspace); err != nil {
			return "", err
		}
	}
	staged, err := os.MkdirTemp("", "abom-landing-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staged)
	if err := stage(workspace, staged); err != nil {
		return "", fmt.Errorf("staging the pack: %w", err)
	}
	// A session is bound when it decides its query, in the pack as it read it, with what deciding laid
	// out. So it is bound here in the pack as it was, with only the queries the session created and
	// the test its scaffold lays out, as locate binds it, and each change is checked against that.
	laid := func(c Change) bool {
		scaffold := customql.TestScaffold(strings.TrimSuffix(path.Base(c.Path), path.Ext(c.Path)))
		return c.Before == "" && c.After != "" && scaffold[c.Path] == string(c.Body)
	}
	for _, c := range changes {
		if c.Before == "" && c.After != "" && (path.Ext(c.Path) == ".ql" || laid(c)) {
			if err := c.apply(staged); err != nil {
				return "", err
			}
		}
	}
	query, tests, err := bindIn(staged, target)
	if err != nil {
		return "", fmt.Errorf("binding the session: %w", err)
	}
	if err := writableIn(staged, query, tests, changes); err != nil {
		return "", err
	}
	// The pack as it will be must bind the session the same way, or what lands is another query.
	for _, c := range changes {
		if err := c.apply(staged); err != nil {
			return "", err
		}
	}
	if q, d, err := boundTo(staged, target); err != nil || q != query || d != tests {
		rel, _ := filepath.Rel(staged, query)
		return "", fmt.Errorf("once the session's changes land, %s is no longer %s with its test: %v",
			filepath.ToSlash(rel), target, err)
	}
	for _, c := range changes {
		if err := c.apply(workspace); err != nil {
			return "", fmt.Errorf("landing %s: %w", c.Path, err)
		}
	}
	rel, err := filepath.Rel(staged, tests)
	return filepath.ToSlash(rel), err
}

// writableIn checks that each change is one a session bound to query and tests in the pack at staged
// could write, or what its test's scaffold lays out.
func writableIn(staged, query, tests string, changes []Change) error {
	b, err := NewBounds(staged, nil)
	if err == nil {
		b, err = b.Bind(query, tests)
	}
	if err != nil {
		return err
	}
	scaffold := customql.TestScaffold(strings.TrimSuffix(filepath.Base(query), ".ql"))
	for _, c := range changes {
		if want, ok := scaffold[c.Path]; ok && c.Before == "" && c.After != "" && string(c.Body) == want {
			continue
		}
		if _, err := b.writable(filepath.Join(staged, filepath.FromSlash(c.Path))); err != nil {
			return fmt.Errorf("%s is not a file the session could write: %w", c.Path, err)
		}
	}
	return nil
}

// holds checks a change against the pack at workspace: a plain path reached through no link, bytes
// that are what the session left, over exactly the file the session read.
func (c Change) holds(workspace string) error {
	clean := path.Clean(c.Path)
	if clean != c.Path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return fmt.Errorf("%q is not a plain relative path", c.Path)
	}
	if c.After != "" && digestOf(c.Body) != c.After {
		return fmt.Errorf("%s is not what the session left there", c.Path)
	}
	parts := strings.Split(c.Path, "/")
	for i := range parts {
		info, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(strings.Join(parts[:i+1], "/"))))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if c.Before != "" {
				return fmt.Errorf("%s changed since the session read it, so nothing the session did lands", c.Path)
			}
			return nil
		case err != nil:
			return err
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is reached through a link, and a session writes only the pack's own files", c.Path)
		case i == len(parts)-1 && !info.Mode().IsRegular():
			return fmt.Errorf("%s is not a file", c.Path)
		}
	}
	body, err := regular.ReadFile(filepath.Join(workspace, filepath.FromSlash(c.Path)))
	if err != nil {
		return err
	}
	if digestOf(body) != c.Before {
		return fmt.Errorf("%s changed since the session read it, so nothing the session did lands", c.Path)
	}
	return nil
}

// apply makes the change under root: the file written whole in place, or removed.
func (c Change) apply(root string) error {
	file := filepath.Join(root, filepath.FromSlash(c.Path))
	if c.After == "" {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".landing-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(c.Body)
	if err = errors.Join(err, tmp.Chmod(0o644), tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// stage copies the pack's own files to dir: not caches, test databases or what a run leaves, which a
// session's bounds are not read from.
func stage(workspace, dir string) error {
	return filepath.WalkDir(workspace, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != workspace && (d.Name() == ".codeql" || d.Name() == ".git" ||
			strings.HasSuffix(d.Name(), ".testproj")):
			return filepath.SkipDir
		case !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".actual"):
			return nil
		}
		rel, err := filepath.Rel(workspace, p)
		if err != nil {
			return err
		}
		body, err := regular.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	})
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
