package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The workflow and job units answer one flag and refuse the other two by name.
func TestAUnitRefusesAFlagItCannotAnswerAndSaysWhereItIsAnswered(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{"/api/workflows?flags=advisory", "/api/jobs?flags=unanalyzed"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want a refusal", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "flags=") || !strings.Contains(body, "dependencies listing") {
			t.Errorf("%s refused without naming the flag and where it is answered: %s", path, body)
		}
		if strings.Contains(body, "relationship or a finding") {
			t.Errorf("%s calls a flag a relationship or a finding: %s", path, body)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workflows?flags=findings", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("the one flag the unit answers was refused: %d %s", rec.Code, rec.Body.String())
	}
}
