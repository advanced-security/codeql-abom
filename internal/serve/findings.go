package serve

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/store"
)

var findingReviewParameters = []string{
	"q", "top", "offset", "view", "kinds", "origins", "minReach", "scope", "flags", "ref",
	"rules", "notRules", "tags", "notTags", "provider", "severity", "securitySeverity", "source",
	"repositories", "mode",
	"properties", "notProperties", "asset", "assetKind", "text",
}

func (s *Server) findingQuery(w http.ResponseWriter, r *http.Request) (store.FindingReviewQuery, bool) {
	query, ok := s.inventoryQuery(w, r, "asset", "assetKind", "text", "evaluation")
	if !ok {
		return store.FindingReviewQuery{}, false
	}
	params := r.URL.Query()
	asset, kind := params.Get("asset"), params.Get("assetKind")
	if kind != "" && kind != "node" && kind != "repository" || (asset == "") != (kind == "") {
		writeError(w, http.StatusBadRequest, "A finding review needs both asset and assetKind (node or repository).")
		return store.FindingReviewQuery{}, false
	}
	return store.FindingReviewQuery{InventoryQuery: query, AssetKey: asset, AssetKind: kind, Text: params.Get("text")}, true
}

func (s *Server) findings(w http.ResponseWriter, r *http.Request) {
	query, ok := s.findingQuery(w, r)
	if !ok {
		return
	}
	page, err := s.Store.InventoryFindingsPage(query)
	if errors.Is(err, store.ErrReachNotDerived) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, page, err)
}

func (s *Server) findingReview(w http.ResponseWriter, r *http.Request) {
	query, ok := s.findingQuery(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "A finding ID must be a positive integer.")
		return
	}
	var evaluation int64
	if value := r.URL.Query().Get("evaluation"); value != "" {
		evaluation, err = strconv.ParseInt(value, 10, 64)
		if err != nil || evaluation <= 0 {
			writeError(w, http.StatusBadRequest, "An evaluation ID must be a positive integer.")
			return
		}
	}
	query.FindingID = id
	details, err := s.Store.FindingReviewDetails(query, evaluation)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "This finding or evaluation is not recorded in the selected scope.")
		return
	}
	writeJSON(w, details, err)
}
