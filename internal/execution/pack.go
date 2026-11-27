package execution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// packLimit bounds the operator's pack, which is source, and databaseLimit one test's database.
	packLimit     = 64 << 20
	databaseLimit = 512 << 20
)

var epoch = time.Unix(0, 0).UTC()

// leaving is what a run writes beside a test, or a cache, which a run never reads as input.
func leaving(d fs.DirEntry) bool {
	if d.IsDir() {
		return d.Name() == ".codeql" || d.Name() == ".git" || strings.HasSuffix(d.Name(), ".testproj")
	}
	return strings.HasSuffix(d.Name(), ".actual")
}

// packBundle is the pack under root as a .tar.gz, in path order with fixed ownership and time, less
// what a run leaves: the worker builds its own.
func packBundle(root string) ([]byte, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p != root && leaving(d) && d.IsDir():
			return filepath.SkipDir
		case d.Type().IsRegular() && !leaving(d):
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tarGz(root, files, packLimit)
}

func tarGz(root string, files []string, limit int64) ([]byte, error) {
	names := make([]string, len(files))
	for i, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil, err
		}
		names[i] = filepath.ToSlash(rel)
	}
	return archive(root, names, func(i int, _ int64) ([]byte, error) { return os.ReadFile(files[i]) }, limit)
}

// archiveIn is the files under dir within pack, slash paths, as a .tar.gz named relative to dir, each
// read through no link and from nothing but a regular file.
func archiveIn(pack *os.Root, dir string, files []string, limit int64) ([]byte, error) {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimPrefix(f, dir+"/")
	}
	return archive(dir, names, func(i int, left int64) ([]byte, error) { return readIn(pack, files[i], left) }, limit)
}

// archive is names, each read by read with what is left of limit, as a .tar.gz in that order with
// fixed ownership and time; what is what an error calls it.
func archive(what string, names []string, read func(i int, left int64) ([]byte, error), limit int64) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var size int64
	for i, name := range names {
		body, err := read(i, limit-size)
		if err != nil {
			return nil, err
		}
		if size += int64(len(body)); size > limit {
			return nil, fmt.Errorf("%s holds more than %d MB", what, limit>>20)
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644,
			Size: int64(len(body)), ModTime: epoch, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// untarGz writes an archive's regular files under dir, refusing any name that would land outside it,
// and any link already in dir that would lead out of it, as the agent's share of a session's can hold.
func untarGz(body []byte, dir string, limit int64) error {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tr := tar.NewReader(gz)
	var size int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a regular file", h.Name)
		}
		if !plain(h.Name) {
			return fmt.Errorf("%q is not a plain relative name", h.Name)
		}
		if size += h.Size; size > limit {
			return fmt.Errorf("the archive holds more than %d MB", limit>>20)
		}
		file := filepath.FromSlash(h.Name)
		if err := root.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		out, err := root.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, h.Size))
		if err := errors.Join(err, out.Close()); err != nil {
			return err
		}
	}
}

// leftIn is the directories a run of tests writes beside: each test directory, and the one
// holding each test file, since the CLI writes a .qlref's output and its directory's database there.
func leftIn(root string, tests []string) []string {
	var dirs []string
	for _, t := range tests {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(t))); err == nil && !info.IsDir() {
			t = path.Dir(t)
		}
		if !slices.Contains(dirs, t) {
			dirs = append(dirs, t)
		}
	}
	return dirs
}

func plain(name string) bool {
	clean := path.Clean(name)
	return clean == name && clean != "." && !strings.HasPrefix(clean, "../") && clean != ".." && !path.IsAbs(clean)
}
