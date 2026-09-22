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
	// Presentation is how the Explorer drew it, carried beside the predicate rather than inside
	// it. Folding `view` or `groupBy` into the filter language would make the server responsible
	// for browser rendering and pad every listing's refusal list with terms that are not filters.
	// Built-ins carry none: a question worth asking is not a claim about how a screen should look.
	Presentation string `json:"presentation,omitempty"`
	Builtin      bool   `json:"builtin"`
	CreatedBy    string `json:"createdBy,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	// ListingRefuses names the terms Inventory cannot narrow by, so a tab that will refuse itself
	// can say so before it is clicked rather than after. A view is saved wherever it was built and
	// drawn wherever it is offered, and the Explorer answers terms a list cannot.
	ListingRefuses []string `json:"listingRefuses,omitempty"`
}

// named describes one catalog entry, working out where it can be drawn from the predicate rather
// than from where it happened to be saved.
func named(name, title, predicate, presentation string, builtin bool, by, at string) NamedView {
	v := NamedView{
		Name: name, Title: title, Predicate: predicate, Presentation: presentation,
		Builtin: builtin, CreatedBy: by, CreatedAt: at,
	}
	if spec, err := viewspec.Parse(predicate); err == nil {
		v.ListingRefuses = spec.UnenforceableAsInventory()
	}
	return v
}

func (s *Server) views(w http.ResponseWriter, r *http.Request) {
	out := []NamedView{}
	for _, b := range viewspec.Builtins {
		out = append(out, named(b.Name, b.Title, b.Predicate, "", true, "", ""))
	}
	saved, err := s.Store.SavedViews()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	for _, v := range saved {
		out = append(out, named(v.Name, "", v.Predicate, v.Presentation, false, v.CreatedBy, v.CreatedAt))
	}
	writeJSON(w, out, nil)
}

func (s *Server) saveView(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		Predicate    string `json:"predicate"`
		Presentation string `json:"presentation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a view is an object with a predicate")
		return
	}
	if err := s.Store.SaveView(store.SavedView{
		Name: r.PathValue("name"), Predicate: body.Predicate, Presentation: body.Presentation,
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
	return "there is no view named " + strconv.Quote(e.name)
}

var errViewNotAlone = errors.New(
	"a named view is the whole question and cannot be combined with other filters")
