package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/bomexport"
	"github.com/advanced-security/codeql-abom/internal/store"
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
	doc, err := s.Store.BillOfMaterials(store.BOMOptions{
		Roots: r.URL.Query()["root"],
	})
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

// SerializeBOM renders the document in one of the supported formats.
//
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
