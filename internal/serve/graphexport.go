package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/graphexport"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// graphGroupings are the groupings a cluster can be drawn for.
//
// Each is a field the projection already carries. A grouping outside this set is refused rather
// than ignored, because a document silently drawn ungrouped looks exactly like one where nothing
// shared a group.
var graphGroupings = map[string]bool{"owner": true, "repository": true, "workflow": true}

// graphFormatting are the parameters that say how to write the answer rather than which answer to
// give. They are removed before the predicate is read, not refused by it.
var graphFormatting = []string{"format", "groupBy", "download"}

// graphExport ships the projection as a document another tool reads.
//
// It answers from the same predicate and the same options as `/api/graph/full`, so an export and
// the picture on screen cannot describe different sets.
func (s *Server) graphExport(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, graphFormatting...)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsGraph(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, undrawable(refused))
		return
	}

	groupBy := r.URL.Query().Get("groupBy")
	if groupBy != "" && !graphGroupings[groupBy] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"%q is not a grouping; use owner, repository or workflow", groupBy))
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "dot"
	}

	opts, err := s.graphOptions(spec)
	if err != nil {
		writeJSON(w, store.Graph{}, err)
		return
	}
	g, err := s.Store.FullGraph(opts)
	if err != nil {
		writeJSON(w, store.Graph{}, err)
		return
	}
	// The terms this payload was not narrowed by travel with it. Nothing downstream of a file
	// applies them, so a document that omitted them would claim to be narrower than it is.
	g.ClientApplied = spec.ClientApplied()

	body, contentType, ext, err := SerializeGraph(g, format, graphexport.Options{
		Predicate: predicateText(r.URL.Query()),
		GroupBy:   groupBy,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
			fmt.Sprintf("abom-graph-%s.%s", time.Now().UTC().Format("20060102-150405"), ext)))
	}
	if _, err := w.Write(body); err != nil && s.Log != nil {
		fmt.Fprintf(s.Log, "writing graph export: %v\n", err)
	}
}

// SerializeGraph renders the projection in one of the supported formats.
//
// Exported for the same reason SerializeBOM is: a document written from the command line and one
// downloaded from the page must not be able to differ.
func SerializeGraph(
	g store.Graph, format string, opts graphexport.Options,
) (body []byte, contentType, ext string, err error) {
	switch format {
	case "native":
		body, err = json.MarshalIndent(g, "", "  ")
		return body, "application/json", "json", err
	case "dot":
		body, err = graphexport.DOT(g, opts)
		return body, "text/vnd.graphviz", "dot", err
	default:
		return nil, "", "", fmt.Errorf(
			"serve: unknown graph format %q; use native or dot", format)
	}
}

// predicateText is the question this document answers, as the reader wrote it.
//
// Reconstructed from the query rather than from the parsed predicate, so a saved file records
// what was asked instead of this package's rendering of it.
func predicateText(q url.Values) string {
	only := url.Values{}
	for key, values := range q {
		only[key] = values
	}
	for _, key := range graphFormatting {
		only.Del(key)
	}
	return only.Encode()
}
