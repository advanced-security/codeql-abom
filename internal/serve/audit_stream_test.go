package serve

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestCancelledAuditDownloadNeverClaimsCompletion(t *testing.T) {
	s, st, _, request := authoringServer(t)
	session, err := st.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z", Hook: "pre-tool-use", Tool: "view", Decision: "allowed"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := localRequest("GET", "/api/authoring-events/export", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), `"complete":true`) {
		t.Fatal("canceled export claimed completeness")
	}
}

func TestUnknownAuditOwnersAreNotSuccessfulEmptyLogs(t *testing.T) {
	s, _, _, _ := authoringServer(t)
	for _, path := range []string{"/api/authoring-events?request=9999", "/api/authoring-events?run=9999"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s answered %d, want missing owner", path, w.Code)
		}
	}
}
