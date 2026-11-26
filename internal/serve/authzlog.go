package serve

import (
	"net/http"
	"strconv"
)

// authzLogPage bounds one read of the authorization log.
const authzLogPage = 500

// authzLog lists the signed authorization log after a sequence number, oldest first. A cursor that
// is not a number is refused rather than read as zero, which would silently restart the walk.
func (s *Server) authzLog(w http.ResponseWriter, r *http.Request) {
	after, limit := int64(0), authzLogPage
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "after is the sequence number of the last entry read")
			return
		}
		after = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit is how many entries to read, at least one")
			return
		}
		limit = min(n, authzLogPage)
	}
	entries, err := s.Store.Assertions(after, limit)
	writeJSON(w, map[string]any{"entries": entries}, err)
}
