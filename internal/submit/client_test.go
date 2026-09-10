package submit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, srv *httptest.Server, allowed ...string) *Client {
	t.Helper()
	c := NewClient("test-token", allowed)
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

// The guard has to exist in code rather than in an operator's memory. A submission cannot be
// withdrawn, and being asked to run a plan is not authorization to write to a third party's
// repository — that inference is what put a fabricated dependency on a live repository.
func TestSubmitRefusesARepositoryThatIsNotAnApprovedTarget(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := testClient(t, srv, "advanced-security/codeql-abom-test1")
	m := meta()
	m.Repository = "advanced-security/some-production-repo"

	_, err := c.Submit(context.Background(), m, Build(m, nil))
	if err == nil {
		t.Fatal("submitting to an unapproved repository succeeded")
	}
	if !strings.Contains(err.Error(), "not an approved") {
		t.Errorf("error = %v, want it to name the allow-list", err)
	}
	if called {
		t.Error("the request reached the server despite the guard")
	}
}

// Defense in depth behind Build: if a manifest ever names a path GitHub analyzes, the submission
// stops being additive and starts replacing GitHub's own findings for that file.
func TestSubmitRefusesToClaimAPathGitHubAnalyzes(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := testClient(t, srv, "advanced-security/codeql-abom-test1")
	m := meta()
	snap := Build(m, nil)
	snap.Manifests["ci"] = Manifest{
		Name: "ci",
		File: ManifestFile{SourceLocation: ".github/workflows/ci.yml"},
	}

	if _, err := c.Submit(context.Background(), m, snap); err == nil {
		t.Fatal("submitting a snapshot that claims a workflow succeeded")
	}
	if called {
		t.Error("the request reached the server despite the guard")
	}
}

func TestSubmitPostsTheSnapshotToTheDependencyGraphEndpoint(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "result": "SUCCESS"})
	}))
	defer srv.Close()

	c := testClient(t, srv, "advanced-security/codeql-abom-test1")
	m := meta()
	res, err := c.Submit(context.Background(), m, Build(m, nil))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if want := "/repos/advanced-security/codeql-abom-test1/dependency-graph/snapshots"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if !strings.Contains(gotBody, `"manifests":{}`) {
		t.Errorf("body did not carry the release manifests object: %s", gotBody)
	}
	if res.Result != "SUCCESS" {
		t.Errorf("result = %q, want SUCCESS", res.Result)
	}
}

// fetch-report answers 202 with an empty body until the report exists. Reading that as an SBOM
// reports a repository with no dependencies, which is indistinguishable from a real empty graph
// and would make the round trip agree with anything.
func TestTheSBOMRoundTripWaitsRatherThanReadingAnEmptyReportAsEmpty(t *testing.T) {
	var fetches int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/generate-report"):
			json.NewEncoder(w).Encode(map[string]string{
				"sbom_url": srv.URL + "/fetch-report/abc-123"})
		default:
			fetches++
			if fetches < 3 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"sbom": map[string]any{
				"packages": []any{map[string]any{"externalRefs": []any{map[string]string{
					"referenceType":    "purl",
					"referenceLocator": "pkg:githubactions/actions/checkout@4.%2A.%2A",
				}}}},
			}})
		}
	}))
	defer srv.Close()

	c := testClient(t, srv, "advanced-security/codeql-abom-test1")
	c.PollInterval = 0

	got, err := c.PackageURLs(context.Background(), "advanced-security/codeql-abom-test1")
	if err != nil {
		t.Fatalf("PackageURLs: %v", err)
	}
	if fetches < 3 {
		t.Errorf("gave up after %d fetches; it must wait for the report", fetches)
	}
	if len(got) != 1 || got[0] != "pkg:githubactions/actions/checkout@4.%2A.%2A" {
		t.Errorf("purls = %v, want the one the report carried", got)
	}
}
