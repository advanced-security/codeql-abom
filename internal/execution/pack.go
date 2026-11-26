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
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var size int64
	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil, err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if size += int64(len(body)); size > limit {
			return nil, fmt.Errorf("%s holds more than %d MB", root, limit>>20)
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: filepath.ToSlash(rel), Mode: 0o644,
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

// untarGz writes an archive's regular files under dir, refusing any name that would land outside it.
func untarGz(body []byte, dir string, limit int64) error {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
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
		file := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
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
