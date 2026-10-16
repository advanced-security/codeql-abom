package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceEligibilityReadsMetadataAndAtMostOneAlert(t *testing.T) {
	var asked []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/acme/repo" {
			fmt.Fprint(w, `{"full_name":"acme/repo","private":true,"archived":true,
				"security_and_analysis":{"code_security":{"status":"enabled"}}}`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer api.Close()
	c := New(api.URL, "", nil)
	meta, err := c.SourceAccess(context.Background(), "acme/repo")
	if err != nil || meta.Visibility != "private" || meta.CodeSecurity != "enabled" {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
	if err := c.CheckScanningAccess(context.Background(), "acme/repo"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || asked[1] != "/repos/acme/repo/code-scanning/alerts?per_page=1" {
		t.Fatalf("requests = %v", asked)
	}
}

func TestMalformedScanningResponsesAreNotPositiveEvidence(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer api.Close()
			if err := New(api.URL, "", nil).CheckScanningAccess(context.Background(), "acme/repo"); err == nil {
				t.Fatal("malformed scanning answer accepted")
			}
		})
	}
}

func TestPublicMetadataCanBeConfirmedWithoutAnOrganizationAuthorizedToken(t *testing.T) {
	for _, visibility := range []string{"public", "private"} {
		t.Run(visibility, func(t *testing.T) {
			requests := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "" {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"message":"Resource protected by organization SAML enforcement."}`)
					return
				}
				fmt.Fprintf(w, `{"full_name":"github/action","visibility":%q}`, visibility)
			}))
			defer api.Close()
			got, err := New(api.URL, "test-token", nil).SourceAccess(context.Background(), "github/action")
			if visibility == "public" {
				if err != nil || got.Visibility != "public" {
					t.Fatalf("public fallback = %+v, %v", got, err)
				}
			} else if err == nil {
				t.Fatal("anonymous fallback accepted a non-public response")
			}
			if requests != 2 {
				t.Fatalf("requests=%d, want authenticated then anonymous", requests)
			}
		})
	}
}
