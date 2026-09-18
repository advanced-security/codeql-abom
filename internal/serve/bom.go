package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/bomexport"
	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// bom answers with a bill of materials for a root set.
//
// `root` may be repeated. Omitting it asks for the declared owned scope, which is the usual case
// and the only one that needs no argument.
//
// `format` selects a serialization. Native ABOM JSON is the default because it is the only one
// with anywhere to put run keys, pack digests and result hashes; the others cannot express
// provenance at this granularity.
func (s *Server) bom(w http.ResponseWriter, r *http.Request) {
	include, exclude, err := documentScope(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc, err := s.Store.BillOfMaterials(store.BOMOptions{
		Roots:         r.URL.Query()["root"],
		Properties:    include,
		NotProperties: exclude,
	})
	// Each of these is a state the reader is in, not a fault the server is in. Reported as a 500
	// they read as a broken server and leave the page unable to say what would fix it.
	if errors.Is(err, store.ErrNoBOMRoot) ||
		errors.Is(err, store.ErrGroupingHasNoRepositories) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "native"
	}
	body, contentType, ext, err := SerializeBOM(doc, format)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	w.Header().Set("Content-Type", contentType)
	// Only when asked. A browser fetching this to render a page must not be handed a download.
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", bomFilename(doc, format, ext)))
	}
	if _, err := w.Write(body); err != nil && s.Log != nil {
		fmt.Fprintf(s.Log, "writing bill of materials: %v\n", err)
	}
}

// documentParams are the parameters this endpoint applies. Anything else is refused by name.
//
// It reads its own parameters rather than the whole predicate, so nothing here would otherwise
// notice a term it does not apply. Measured on a crawled organization: `units=3`, the term
// `properties` replaced, was answered with all 25 roots — a saved link handing a reader the whole
// organization under the name of one part of it.
var documentParams = map[string]bool{
	"root": true, "format": true, "download": true,
	"properties": true, "notProperties": true,
}

// documentScope refuses a parameter this endpoint does not apply, then reads the grouping terms.
func documentScope(q url.Values) (include, exclude []viewspec.PropertyMatch, err error) {
	for key := range q {
		if !documentParams[key] {
			return nil, nil, fmt.Errorf(
				"a bill of materials does not narrow by %q; it is not a term this document is about", key)
		}
	}
	return propertyMatches(q)
}

// propertyMatches reads the grouping terms through the shared parser rather than beside it, so a
// malformed term is refused in the same words the graph and the gate refuse it in.
func propertyMatches(q url.Values) (include, exclude []viewspec.PropertyMatch, err error) {
	only := url.Values{}
	for _, key := range []string{"properties", "notProperties"} {
		if values, ok := q[key]; ok {
			only[key] = values
		}
	}
	spec, err := viewspec.Parse(only.Encode())
	if err != nil {
		return nil, nil, err
	}
	return spec.Properties, spec.NotProperties, nil
}

// SerializeBOM renders the document in one of the supported formats.//
// Exported so the `bom` command and this endpoint share one implementation: a document written
// from the command line and one downloaded from the page must not be able to differ.
func SerializeBOM(doc store.BOM, format string) (body []byte, contentType, ext string, err error) {
	switch format {
	case "native":
		body, err = json.MarshalIndent(doc, "", "  ")
		return body, "application/json", "json", err
	case "cyclonedx":
		body, err = bomexport.CycloneDX(doc)
		return body, "application/vnd.cyclonedx+json", "cdx.json", err
	case "spdx":
		body, err = bomexport.SPDX(doc)
		return body, "application/spdx+json", "spdx.json", err
	default:
		return nil, "", "", fmt.Errorf(
			"serve: unknown bill of materials format %q; use native, cyclonedx or spdx", format)
	}
}

// bomFilename names the download after what it is about, so several saved documents stay
// distinguishable without opening them.
func bomFilename(doc store.BOM, format, ext string) string {
	subject := "owned-scope"
	if len(doc.Roots) == 1 {
		subject = strings.ReplaceAll(doc.Roots[0], "/", "-")
	} else if len(doc.Roots) > 1 {
		subject = fmt.Sprintf("%d-roots", len(doc.Roots))
	}
	stamp := strings.ReplaceAll(doc.GeneratedAt, ":", "")
	if t, err := time.Parse(time.RFC3339, doc.GeneratedAt); err == nil {
		stamp = t.UTC().Format("20060102T150405Z")
	}
	return fmt.Sprintf("abom-%s-%s-%s.%s", subject, format, stamp, ext)
}
