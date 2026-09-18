package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Lock serializes CodeQL invocations across every process on the machine.
//
// CodeQL keeps one compilation and dependency cache per machine, shared by every invocation. Two
// evaluations running against it at once can return wrong answers to both, which is a far worse
// outcome than being slow: the pipeline may fetch four repositories at a time, but only one of
// them may be inside CodeQL.
//
// Two halves, because neither is sufficient. The mutex serializes this process's own workers,
// which is the common case. The file lock serializes against a second abom process, and is
// released by the kernel if that process dies, which a lock file created with O_EXCL would not
// be.
//
// It does not make `make ql` or the CodeQL MCP server cooperate: they take no lock. Those stay a
// documented rule, and this covers what the pipeline itself controls.
type Lock struct {
	// Path names the lock file. Empty means no cross-process locking, which is what unit tests
	// and one-shot commands want.
	Path string

	mu   sync.Mutex
	file *os.File
}

// SharedLock is the lock every abom process takes before invoking CodeQL.
//
// One path per user per machine, matching the scope of the compilation cache it protects, so a
// crawl and a hand-run `expand-all` serialize against each other rather than corrupting it.
//
// Where there is no cache directory there is no correct place for that lock, and it returns one
// that only serializes this process. Claiming a narrower scope than the cache has would be worse
// than claiming none: it reads as protection and is not.
func SharedLock() *Lock {
	dir, err := os.UserCacheDir()
	if err != nil {
		return &Lock{}
	}
	return &Lock{Path: filepath.Join(dir, "codeql-abom", "codeql.lock")}
}

// Acquire blocks until the caller may invoke CodeQL, returning the release.
//
// A nil Lock acquires immediately, so a Runner without one still works.
func (l *Lock) Acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := l.lockProcess(ctx); err != nil {
		return nil, err
	}
	if l.Path == "" {
		return l.mu.Unlock, nil
	}

	if err := l.lockFile(ctx); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	return func() {
		if l.file != nil {
			// Unlock before closing: closing drops the lock anyway, but doing it explicitly
			// keeps the failure visible rather than implicit.
			_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
			l.file.Close()
			l.file = nil
		}
		l.mu.Unlock()
	}, nil
}

// lockProcess takes the in-process half without ignoring cancellation, which sync.Mutex alone
// cannot do. Stopping discovery must not have to wait out a database build.
func (l *Lock) lockProcess(ctx context.Context) error {
	acquired := make(chan struct{})
	go func() {
		l.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		// The goroutine still holds the mutex once it wins, so hand it straight back rather
		// than leaking a hold nobody will release.
		go func() {
			<-acquired
			l.mu.Unlock()
		}()
		return ctx.Err()
	}
}

func (l *Lock) lockFile(ctx context.Context) error {
	dir, name := filepath.Split(l.Path)
	if name == "" {
		return fmt.Errorf("%q names a directory rather than a lock file", l.Path)
	}
	if dir == "" {
		dir = "."
	}
	// Opened relative to the directory rather than by its whole path. `Path` is derived from the
	// user's cache directory, which comes from the environment, and O_CREATE|O_RDONLY follows a
	// symlink silently — so a link planted at the lock's name would flock a file elsewhere on the
	// machine. A root confines the open to the directory ABOM chose, and a single-element name
	// cannot traverse out of it.
	root, err := openLockDir(dir)
	if err != nil {
		return err
	}
	// The root is only a handle for the open below; the locked file outlives it.
	defer func() { _ = root.Close() }()

	// Read-only: nothing is ever written to the lock file, only flocked. Opening it for writing
	// would claim an access this never uses and make every close a potential lost write.
	f, err := root.OpenFile(name, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return pathFault("opening", l.Path, err)
	}

	// Non-blocking, polled, so cancellation is observed. A blocking flock would ignore ctx.
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			l.file = f
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return fmt.Errorf("locking %q: %w", l.Path, err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// lockPollInterval trades a little latency for cancellability. A CodeQL invocation runs for
// seconds at least, so waking 20 times a second costs nothing against it.
const lockPollInterval = 50 * time.Millisecond

// openLockDir returns a root confined to the directory the lock file lives in, creating the one
// element ABOM owns along the way.
//
// Two different trust decisions, and they are not the same. The cache root is the *user's*: they
// named it through the environment, and symlinking `~/.cache` onto another disk is an ordinary
// thing to do, so a link there is followed. ABOM's own directory inside it is ABOM's, so a link
// standing where that belongs was put there by somebody else and is refused -- `Mkdir` reporting
// ErrExist says only that the name is taken, and reopening it by name afterwards would follow it
// straight out of the cache. `Root.OpenRoot` will not, which is why the parent stays open.
func openLockDir(dir string) (*os.Root, error) {
	clean := filepath.Clean(dir)
	parent, leaf := filepath.Split(clean)
	if leaf == "" || leaf == "." || leaf == string(filepath.Separator) {
		// Nothing of ABOM's own to create; the directory is one the caller already chose.
		return os.OpenRoot(clean)
	}
	if parent == "" {
		parent = "."
	}

	parentRoot, err := openParentDir(filepath.Clean(parent))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parentRoot.Close() }()

	if err := parentRoot.Mkdir(leaf, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, pathFault("creating the lock directory", clean, err)
	}
	root, err := parentRoot.OpenRoot(leaf)
	if err != nil {
		return nil, pathFault("opening the lock directory", clean, err)
	}
	return root, nil
}

// openParentDir opens the lock directory's parent, creating it when the account is new enough not
// to have a cache root yet.
//
// `os.MkdirAll` would do this in one call and resolve every element by name as it went, following
// any symlink it met and re-resolving the prefix between one element and the next. The path comes
// from the environment, so that is both a way out of the cache and a race: a link dropped where a
// missing element belongs, at the moment between creating it and creating its child, relocates
// the whole tree. Anchoring a root at the deepest directory that already exists and building
// downward through it closes both -- a root holds the directory open rather than re-resolving it,
// and refuses any link that leaves it.
func openParentDir(parent string) (*os.Root, error) {
	root, err := os.OpenRoot(parent)
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, pathFault("opening the lock directory's parent", parent, err)
	}

	anchor, rest, err := deepestExisting(parent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = anchor.Close() }()

	if err := anchor.MkdirAll(rest, 0o755); err != nil {
		return nil, pathFault("creating the lock directory's parent", parent, err)
	}
	root, err = anchor.OpenRoot(rest)
	if err != nil {
		return nil, pathFault("opening the lock directory's parent", parent, err)
	}
	return root, nil
}

// deepestExisting opens the nearest ancestor of path that exists and names the remainder relative
// to it, so the caller can create that remainder inside a root rather than by absolute path.
func deepestExisting(path string) (*os.Root, string, error) {
	rest := ""
	for probe := path; ; {
		parent, leaf := filepath.Split(probe)
		if leaf == "" {
			return nil, "", fmt.Errorf("no existing directory above %q", path)
		}
		rest = filepath.Join(leaf, rest)

		parent = filepath.Clean(parent)
		root, err := os.OpenRoot(parent)
		if err == nil {
			return root, rest, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, "", pathFault("opening", parent, err)
		}
		if parent == probe {
			return nil, "", fmt.Errorf("no existing directory above %q", path)
		}
		probe = parent
	}
}

// pathFault names a filesystem failure without letting the path it happened to forge a line.
//
// `*fs.PathError` prints its own path raw, and every path here descends from the cache directory,
// which the environment names. A newline in one would print as what looks like a second line of
// output. Only the underlying cause is wrapped, and the path is quoted.
func pathFault(doing, path string, err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		err = perr.Err
	}
	return fmt.Errorf("%s %q: %w", doing, path, err)
}
