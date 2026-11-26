package serve

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/publish"
	"github.com/advanced-security/codeql-abom/internal/querypack"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// queryComponents is everything that makes a query run, with the newest test run on record.
type queryComponents struct {
	querypack.Manifest
	// NewestTest is the newest test run of any version of the query, under any CodeQL; null when
	// nothing has tested it. Its version says whether it describes the one on disk.
	NewestTest *store.QueryTest `json:"newestTest"`
	// Exportable is whether a publisher App is configured. ExportTarget is the one repository an
	// export would go to, named before anyone presses, and ExportBlocked why the installation refuses
	// one now; neither is said when GitHub could not be asked, which the export itself then says.
	Exportable    bool   `json:"exportable"`
	ExportTarget  string `json:"exportTarget"`
	ExportBlocked string `json:"exportBlocked"`
}

// componentsOf reads one query's components, answering for the request when it cannot.
func (s *Server) componentsOf(w http.ResponseWriter, r *http.Request) (querypack.Manifest, bool) {
	id := r.URL.Query().Get("query")
	switch {
	case s.QueryDir == "":
		writeProblem(w, problemNoQueryDirectory,
			"this server has no query directory to read a query from; start it with --custom-ql-dir")
		return querypack.Manifest{}, false
	case id == "":
		writeError(w, http.StatusBadRequest, "name the query whose components to show")
		return querypack.Manifest{}, false
	}
	q, found, err := authoring.Find(s.QueryDir, id)
	switch {
	case err != nil:
		writeJSON(w, nil, err)
		return querypack.Manifest{}, false
	case !found:
		writeProblem(w, problemQueryNotFound, id+" is not a query in the query directory")
		return querypack.Manifest{}, false
	}
	m, err := querypack.Read(s.QueryDir, q)
	if err != nil {
		writeJSON(w, nil, err)
		return querypack.Manifest{}, false
	}
	return m, true
}

// queryComponentsList serves a query's components manifest.
func (s *Server) queryComponentsList(w http.ResponseWriter, r *http.Request) {
	m, ok := s.componentsOf(w, r)
	if !ok {
		return
	}
	tests, err := s.Store.QueryTestsOf(m.Query)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := queryComponents{Manifest: m, Exportable: s.Publisher != nil}
	if len(tests) > 0 {
		out.NewestTest = &tests[len(tests)-1]
	}
	if s.Publisher != nil {
		target, err := s.Publisher.Target(r.Context())
		var misinstalled *publish.Misinstalled
		switch {
		case err == nil:
			out.ExportTarget = target.String()
		case errors.As(err, &misinstalled):
			out.ExportBlocked = err.Error()
		}
	}
	writeJSON(w, out, nil)
}

// queryComponent opens one component by its path. Only a path the query's components name is read,
// so the request never chooses a file.
func (s *Server) queryComponent(w http.ResponseWriter, r *http.Request) {
	m, ok := s.componentsOf(w, r)
	if !ok {
		return
	}
	asked := path.Clean(r.URL.Query().Get("path"))
	for _, c := range m.Components {
		if c.Path != asked {
			continue
		}
		// Read again through the pack's root: a file swapped for a link since is never followed.
		body, err := querypack.ReadFile(s.QueryDir, c.Path)
		var refused *querypack.Refused
		switch {
		case errors.As(err, &refused):
			writeProblem(w, problemComponentChanged, c.Path+" is no longer a regular file; read the components again")
		case errors.Is(err, fs.ErrNotExist):
			writeProblem(w, problemComponentChanged, c.Path+" is no longer there; read the components again")
		case err != nil:
			writeJSON(w, nil, err)
		default:
			writeJSON(w, s.stepFileOf(filepath.Join(s.QueryDir, filepath.FromSlash(c.Path)), body), nil)
		}
		return
	}
	writeError(w, http.StatusNotFound, asked+" is not one of "+m.Query+"'s components")
}

// queryBundle downloads a query's bundle, with the digest its components were shown with.
func (s *Server) queryBundle(w http.ResponseWriter, r *http.Request) {
	m, ok := s.componentsOf(w, r)
	if !ok {
		return
	}
	if why := m.Unbundled(); why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	var body strings.Builder
	var changed *querypack.ComponentError
	if err := querypack.Write(&body, s.QueryDir, m); errors.As(err, &changed) {
		writeProblem(w, problemComponentChanged, err.Error()+"; read the components again")
		return
	} else if err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+m.ExportName()+`.tar.gz"`)
	w.Header().Set("X-Abom-Bundle-Sha256", m.BundleSHA256)
	_, _ = w.Write([]byte(body.String()))
}
