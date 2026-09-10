package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// NamedView is one entry in the catalog: a built-in that ships with the product, or one the
// operator saved.
//
// The `builtin` flag is what tells a reader which they are looking at, and it is the whole reason
// both are listed through one endpoint: a caller asking "what questions can I ask" should not have
// to consult two places and merge them.
type NamedView struct {
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Predicate string `json:"predicate"`
	Builtin   bool   `json:"builtin"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

func (s *Server) views(w http.ResponseWriter, r *http.Request) {
	out := []NamedView{}
	for _, b := range viewspec.Builtins {
		out = append(out, NamedView{
			Name: b.Name, Title: b.Title, Predicate: b.Predicate, Builtin: true,
		})
	}
	saved, err := s.Store.SavedViews()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	for _, v := range saved {
		out = append(out, NamedView{
			Name: v.Name, Predicate: v.Predicate,
			CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
		})
	}
	writeJSON(w, out, nil)
}

func (s *Server) saveView(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		Predicate string `json:"predicate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a view is an object with a predicate")
		return
	}
	if err := s.Store.SaveView(store.SavedView{
		Name: r.PathValue("name"), Predicate: body.Predicate,
		CreatedBy: store.DeclaredByInterface,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.views(w, r)
}

func (s *Server) deleteView(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if err := s.Store.DeleteView(r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.views(w, r)
}

// resolveView turns `?view=<name>` into the predicate it names.
//
// A name is the whole question, so it may not be combined with ad-hoc predicate parameters: two
// predicates in one request have no defined precedence, and silently merging them is exactly the
// ambiguity this language exists to remove. An unknown name is refused for the same reason an
// unknown filter is — a view that quietly matched everything would fail a pull request on a
// question nobody asked.
func (s *Server) resolveView(r *http.Request, cursors ...string) (viewspec.Spec, bool, error) {
	q := r.URL.Query()
	for _, key := range cursors {
		q.Del(key)
	}
	name := q.Get(viewNameParam)
	if name == "" {
		return viewspec.Spec{}, false, nil
	}
	q.Del(viewNameParam)
	if len(q) > 0 {
		return viewspec.Spec{}, true, errViewNotAlone
	}
	if b, ok := viewspec.BuiltinNamed(name); ok {
		spec, err := viewspec.Parse(b.Predicate)
		return spec, true, err
	}
	saved, ok, err := s.Store.SavedViewNamed(name)
	if err != nil {
		return viewspec.Spec{}, true, err
	}
	if !ok {
		return viewspec.Spec{}, true, &unknownViewError{name: name}
	}
	spec, err := viewspec.Parse(saved.Predicate)
	return spec, true, err
}

// viewNameParam names a saved view. Deliberately not part of `view.Spec`: a spec is a predicate,
// and this is a reference to one.
const viewNameParam = "view"

type unknownViewError struct{ name string }

func (e *unknownViewError) Error() string {
	return "view: no view named " + strconv.Quote(e.name)
}

var errViewNotAlone = errors.New(
	"view: a named view is the whole question and cannot be combined with other filters")
