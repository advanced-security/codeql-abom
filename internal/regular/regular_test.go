package regular

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A pipe holds a read open until something writes to it, and what another process can write may be
// one by the time it is opened.
func TestAPipeIsRefusedWithoutWaitingForAWriter(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "X.ql"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	opens := map[string]func() error{
		"Open":     func() error { f, err := Open(filepath.Join(dir, "X.ql")); closed(f); return err },
		"ReadFile": func() error { _, err := ReadFile(filepath.Join(dir, "X.ql")); return err },
		"OpenIn":   func() error { f, err := OpenIn(root, "X.ql"); closed(f); return err },
	}
	for name, open := range opens {
		done := make(chan error, 1)
		go func() { done <- open() }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s opened a pipe as a file", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s waited on a pipe for a writer", name)
		}
	}
}

// Outside a root a link is followed, as os.Open follows it, to what it names. Within one, a name
// that is itself a link is refused, wherever it leads: the root follows a link within it whatever the
// flags say, and refuses only one leading out.
func TestOnlyWithinARootIsALinkRefused(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "tls.key")
	for name, body := range map[string]string{filepath.Join(dir, "A.ql"): "select 1", secret: "PRIVATE KEY"} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"B.ql": "A.ql", "C.ql": secret} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	if body, err := ReadFile(filepath.Join(dir, "B.ql")); err != nil || string(body) != "select 1" {
		t.Errorf("a link outside a root reads %q, %v", body, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, link := range []string{"B.ql", "C.ql"} {
		if f, err := OpenIn(root, link); err == nil {
			closed(f)
			t.Errorf("%s, a link, was opened within the root", link)
		}
	}
	f, err := OpenIn(root, "A.ql")
	if err != nil {
		t.Fatalf("a regular file within the root: %v", err)
	}
	closed(f)
}

// A directory is not a file a caller reads.
func TestADirectoryIsNotOpenedAsAFile(t *testing.T) {
	if f, err := Open(t.TempDir()); err == nil {
		closed(f)
		t.Error("a directory was opened as a file")
	}
}

func closed(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
