package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The endpoint is the only way a document leaves ABOM, so its contract is the product's contract.
func TestTheEndpointServesTheNativeDocumentByDefault(t *testing.T) {
	body, res := getBOM(t, "/api/bom")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, body)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var doc struct {
		Roots      []string `json:"roots"`
		Components []struct {
			Key string `json:"key"`
		} `json:"components"`
		Coverage struct {
			ComponentsInUse int `json:"componentsInUse"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the endpoint returned something that is not JSON: %v", err)
	}
	if len(doc.Roots) == 0 {
		t.Error("the document does not name its own roots")
	}
	if len(doc.Components) == 0 {
		t.Error("the document has no components")
	}
	if doc.Coverage.ComponentsInUse == 0 {
		t.Error("coverage did not travel with the document")
	}
}

func TestTheEndpointServesCycloneDXAndSPDX(t *testing.T) {
	for _, c := range []struct{ format, contentType, marker string }{
		{"cyclonedx", "application/vnd.cyclonedx+json", `"bomFormat"`},
		{"spdx", "application/spdx+json", `"spdxVersion"`},
	} {
		body, res := getBOM(t, "/api/bom?format="+c.format)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", c.format, res.Code, body)
		}
		if ct := res.Header().Get("Content-Type"); ct != c.contentType {
			t.Errorf("%s: Content-Type = %q, want %q", c.format, ct, c.contentType)
		}
		if !strings.Contains(body, c.marker) {
			t.Errorf("%s: the response does not look like %s", c.format, c.format)
		}
	}
}

// An unknown format must be refused rather than silently answered in some other one, or a
// caller asking for a format ABOM cannot write gets a document it will misread.
func TestAnUnknownFormatIsRefused(t *testing.T) {
	body, res := getBOM(t, "/api/bom?format=spdx3")

	if res.Code == http.StatusOK {
		t.Errorf("an unknown format was answered with 200: %s", body)
	}
}

// A browser rendering the page must not be handed a download, so the header is opt-in.
func TestTheDownloadHeaderIsOnlySentWhenAsked(t *testing.T) {
	if _, res := getBOM(t, "/api/bom"); res.Header().Get("Content-Disposition") != "" {
		t.Error("a plain read was answered with a download header")
	}

	_, res := getBOM(t, "/api/bom?download=true")
	cd := res.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	if !strings.Contains(cd, "abom-") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q, want a named .json file", cd)
	}
}

// A document for one repository is a different document, and asking for one is how the interface
// offers "the BOM for this action" beside "the BOM for everything we own".
func TestARootCanBeNamedExplicitly(t *testing.T) {
	body, res := getBOM(t, "/api/bom?root=octo-org/app")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, body)
	}
	var doc struct {
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(doc.Roots) != 1 || doc.Roots[0] != "octo-org/app" {
		t.Errorf("Roots = %v, want only the one asked for", doc.Roots)
	}
}

func getBOM(t *testing.T, target string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	srv, s := newServer(t)
	seedGraph(t, s)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo-org", Kind: store.AccountOrganization, Enabled: true},
	}); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		t.Fatalf("ClassifyEdgeBoundaries: %v", err)
	}

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, target, nil))
	return res.Body.String(), res
}
