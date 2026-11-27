// Package regular opens files that a process ABOM does not trust can write: only regular files, and
// never in a way a pipe or a device can hold open.
package regular

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// Open opens the regular file at name for reading, following a link as os.Open does, and refuses a
// pipe, a device or a directory without waiting on it.
func Open(name string) (*os.File, error) {
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return opened(f, name, nil)
}

// ReadFile is os.ReadFile, through Open.
func ReadFile(name string) ([]byte, error) {
	f, err := Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// OpenIn opens the regular file at name within root as Open does, refusing a name that is itself a
// link: the root refuses one leading out of it, and follows one within it whatever the flags say.
func OpenIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return opened(f, name, func() (fs.FileInfo, error) { return root.Lstat(name) })
}

// opened keeps f only if it is a regular file and, where there says what name is now, that very file.
func opened(f *os.File, name string, there func() (fs.FileInfo, error)) (*os.File, error) {
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", name)
	}
	if err == nil && there != nil {
		var now fs.FileInfo
		if now, err = there(); err == nil && (!now.Mode().IsRegular() || !os.SameFile(info, now)) {
			err = fmt.Errorf("%s is not a regular file", name)
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
