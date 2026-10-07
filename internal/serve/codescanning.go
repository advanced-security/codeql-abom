package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// codeScanningReadings classifies every repository under review for Actions code scanning, with the
// evidence for each class. An unknown class is refused: an empty list would read as "none".
func (s *Server) codeScanningReadings(w http.ResponseWriter, r *http.Request) {
	class := r.URL.Query().Get("class")
	if class != "" && !slices.Contains(store.SetupClassOrder, class) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("class %q is not one of %v", class, store.SetupClassOrder))
		return
	}
	readings, err := setup.Classify(s.Store, class)
	// The counts alone, for a page that has no use for every repository's row.
	if r.URL.Query().Get("summary") == "true" {
		readings.Items = []setup.Reading{}
	}
	writeJSON(w, readings, err)
}

// codeScanningWrites says whether this server writes default setup, and to what.
func (s *Server) codeScanningWrites(w http.ResponseWriter, _ *http.Request) {
	sv := s.CodeScanningWrites
	out := struct {
		Writes   bool     `json:"writes"`
		Targets  []string `json:"targets"`
		Accounts []string `json:"accounts"`
	}{Targets: []string{}, Accounts: []string{}}
	if sv != nil {
		out.Writes = sv.Writes()
		out.Targets = append(out.Targets, sv.Targets...)
		out.Accounts = append(out.Accounts, sv.Accounts...)
	}
	writeJSON(w, out, nil)
}

// createPreview classifies a view's repositories as a batch from it would write, asking GitHub
// nothing. A view the repositories unit cannot apply whole is refused rather than widened.
func (s *Server) createPreview(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) || !s.writesOffered(w) {
		return
	}
	var body struct {
		View    string   `json:"view"`
		Exclude []string `json:"exclude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a preview is an object naming a view and the repositories it leaves out")
		return
	}
	req := setup.PreviewRequest{ViewName: body.View, Exclude: body.Exclude, CreatedBy: store.DeclaredByInterface}
	if body.View != "" {
		predicate, spec, err := s.namedPredicate(body.View)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if refused := spec.UnenforceableAsRepositories(); len(refused) > 0 {
			writeError(w, http.StatusBadRequest, "the view "+body.View+" asks what a list of repositories cannot apply. "+
				unlistable(refused))
			return
		}
		repos, err := s.Store.RepositoriesFor(repositoriesQuery(spec))
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if len(repos) == 0 {
			writeError(w, http.StatusBadRequest, "the view "+body.View+" lists no repositories, so there is nothing to preview")
			return
		}
		req.Predicate, req.Repositories = predicate, repos
	}
	preview, err := s.CodeScanningWrites.Preview(req)
	preview.Rows = nil
	writeJSON(w, preview, err)
}

// namedPredicate reads a builtin or saved view's predicate.
func (s *Server) namedPredicate(name string) (string, viewspec.Spec, error) {
	predicate := ""
	if b, ok := viewspec.BuiltinNamed(name); ok {
		predicate = b.Predicate
	} else {
		saved, ok, err := s.Store.SavedViewNamed(name)
		if err != nil {
			return "", viewspec.Spec{}, err
		}
		if !ok {
			return "", viewspec.Spec{}, &unknownViewError{name: name}
		}
		predicate = saved.Predicate
	}
	spec, err := viewspec.Parse(predicate)
	return predicate, spec, err
}

func (s *Server) writesOffered(w http.ResponseWriter) bool {
	if s.CodeScanningWrites == nil {
		writeError(w, http.StatusServiceUnavailable, "this build offers no default setup writes")
		return false
	}
	return true
}

func (s *Server) previews(w http.ResponseWriter, _ *http.Request) {
	out, err := s.Store.SetupPreviews()
	writeJSON(w, out, err)
}

// preview answers a preview's counts without its rows, which are paged: one per repository in
// scope is every repository an organization declares.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, found, err := s.Store.SetupPreviewOf(id)
	if err == nil && !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("there is no preview %d", id))
		return
	}
	p.Rows = nil
	writeJSON(w, p, err)
}

// previewRows pages a preview's rows, the ones a batch would write first.
func (s *Server) previewRows(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	page, err := s.Store.SetupPreviewRowsPage(id, min(max(intParam(r, "top", 50), 1), 500),
		max(intParam(r, "offset", 0), 0))
	if err == nil && page.Total == 0 {
		if _, found, ferr := s.Store.SetupPreviewOf(id); ferr == nil && !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("there is no preview %d", id))
			return
		}
	}
	writeJSON(w, page, err)
}

// approvePreview records the write token's owner as approving: the identity GitHub will record
// for every write the batch makes.
func (s *Server) approvePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !s.allowControl(w, r) || !s.writesOffered(w) {
		return
	}
	if !s.CodeScanningWrites.Writes() {
		writeError(w, http.StatusConflict, "this server writes nothing, so it approves nothing; start it with "+
			"--enable-targets or --enable-accounts to write")
		return
	}
	if err := s.CodeScanningWrites.Approve(r.Context(), id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.preview(w, r)
}

func (s *Server) batches(w http.ResponseWriter, _ *http.Request) {
	out, err := s.Store.SetupBatches()
	writeJSON(w, out, err)
}

func (s *Server) batch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, found, err := s.Store.SetupBatchOf(id)
	if err == nil && !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("there is no batch %d", id))
		return
	}
	writeJSON(w, b, err)
}

func (s *Server) startBatch(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) || !s.writesOffered(w) {
		return
	}
	var body struct {
		Preview int64 `json:"preview"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Preview <= 0 {
		writeError(w, http.StatusBadRequest, "a batch is started from a preview: {\"preview\": <id>}")
		return
	}
	id, err := s.CodeScanningWrites.StartBatch(body.Preview, store.DeclaredByInterface)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	b, _, err := s.Store.SetupBatchOf(id)
	writeJSON(w, b, err)
}

func (s *Server) actOnBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !s.allowControl(w, r) || !s.writesOffered(w) {
		return
	}
	if err := s.CodeScanningWrites.Act(id, r.PathValue("action")); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.batch(w, r)
}

// ledger lists the newest steps writes took and counts them all, for a repository, a batch, both or neither.
func (s *Server) ledger(w http.ResponseWriter, r *http.Request) {
	batch := int64(0)
	if v := r.URL.Query().Get("batch"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "batch must be a batch's number, got "+strconv.Quote(v))
			return
		}
		batch = n
	}
	out, err := s.Store.SetupLedgerNewest(r.URL.Query().Get("repository"), batch,
		min(max(intParam(r, "top", 50), 1), 500))
	writeJSON(w, out, err)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "not a number: "+strconv.Quote(r.PathValue("id")))
		return 0, false
	}
	return id, true
}
