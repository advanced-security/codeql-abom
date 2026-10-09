package serve

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/authoring"
)

// stepFileLimit keeps one opened file from becoming the whole response.
const stepFileLimit = 512 << 10

type stepFile struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// queryStep sends what a step of a query on disk was read from, so a reader can open the step
// rather than take its mark on trust. Paths come from the working copy, never from the request.
func (s *Server) queryStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.URL.Query().Get("query"), r.URL.Query().Get("step")
	switch {
	case s.QueryDir == "":
		writeError(w, http.StatusServiceUnavailable, "this server has no query directory to read a step "+
			"from; start it with --custom-ql-dir")
		return
	case id == "":
		writeError(w, http.StatusBadRequest, "name the query whose step to show")
		return
	case !stepShowable(step):
		writeError(w, http.StatusBadRequest, "only help, expected, ast, compiles and tests have a file "+
			"to show, not "+step)
		return
	}
	q, found, err := authoring.Find(s.QueryDir, id)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	case !found:
		writeError(w, http.StatusNotFound, id+" is not a query in the query directory")
		return
	}
	files, absent, err := s.stepFiles(q, step)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"step": step, "files": files, "absent": absent}, nil)
}

func stepShowable(step string) bool {
	switch step {
	case "help", "expected", "ast", "compiles", "tests":
		return true
	}
	return false
}

func (s *Server) stepFiles(q authoring.Query, step string) ([]stepFile, string, error) {
	var paths []string
	base := strings.TrimSuffix(q.Path, ".ql")
	switch step {
	case "ast":
		return s.newestTree(q)
	case "compiles":
		paths = []string{q.Path}
	case "help":
		paths = []string{base + ".md", base + ".qhelp"}
	case "expected", "tests":
		ext := map[string]string{"expected": ".expected", "tests": ".actual"}[step]
		for _, test := range q.Tests {
			paths = append(paths, strings.TrimSuffix(test, ".qlref")+ext)
		}
	}
	files := []stepFile{}
	for _, p := range paths {
		f, ok, err := s.readStepFile(p)
		if err != nil {
			return nil, "", err
		}
		if ok {
			files = append(files, f)
			if step == "help" {
				break
			}
		}
	}
	if len(files) > 0 {
		return files, "", nil
	}
	switch step {
	case "help":
		return files, "no " + s.rel(base+".md") + " beside the query", nil
	case "tests":
		return files, "no .actual beside the tests: they have not run, or they passed and CodeQL removed it", nil
	case "expected":
		if len(q.Tests) == 0 {
			return files, "no test names " + q.Rel, nil
		}
		return files, "no .expected has been written beside the tests", nil
	}
	return files, s.rel(q.Path) + " is missing", nil
}

func (s *Server) readStepFile(path string) (stepFile, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return stepFile{}, false, nil
	}
	if err != nil {
		return stepFile{}, false, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, stepFileLimit+1))
	if err != nil {
		return stepFile{}, false, err
	}
	out := stepFile{Path: s.rel(path), Truncated: len(body) > stepFileLimit}
	out.Body = string(body[:min(len(body), stepFileLimit)])
	return out, true, nil
}

func (s *Server) newestTree(q authoring.Query) ([]stepFile, string, error) {
	newest, err := s.Store.NewestASTs()
	if err != nil {
		return nil, "", err
	}
	kept, ok := newest[q.ID]
	if !ok {
		return []stepFile{}, "no PrintAST tree has been printed for this query's test database", nil
	}
	tree, err := s.Store.AuthoringAST(kept.RowID)
	if err != nil {
		return nil, "", err
	}
	path := "PrintAST over " + tree.Database
	if tree.SourceFiles != "" {
		path += " for " + tree.SourceFiles
	}
	return []stepFile{{Path: path, Body: tree.Body, Truncated: tree.Truncated}}, "", nil
}

func (s *Server) rel(path string) string {
	rel, err := filepath.Rel(s.QueryDir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
