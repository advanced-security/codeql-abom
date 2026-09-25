package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const onePrediction = `{"queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],` +
	`"by":"operator","prediction":true}`

func startRefusalWith(t *testing.T, s *Server, query, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.startRun(w, httptest.NewRequest("POST", "/api/runs?"+query, bytes.NewBufferString(body)))
	return w.Code, w.Body.String()
}

// A prediction is asked for the way a run is, over the same resolution, and is recorded as one: the
// reader who reads it back must be able to tell a count of what would be reported from a count of
// what was stored.
func TestAPredictionIsAskedForLikeARunAndRecordedAsOne(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)

	run, code := start(t, s, "rules=actions%2F*", onePrediction)
	if code != http.StatusOK {
		t.Fatalf("asking for a prediction answered %d", code)
	}
	if !run.Prediction {
		t.Error("the recorded run does not say it is a prediction")
	}
	plain, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != http.StatusOK || plain.Prediction {
		t.Errorf("a run nobody asked to predict answered %d and reads prediction=%v", code, plain.Prediction)
	}
}

// Default Setup stages nothing, so a prediction from extended databases would name alerts that will
// never appear. The refusal says why, so the reader knows which shape to ask for instead.
func TestAPredictionFromExtendedDatabasesIsRefusedWithWhy(t *testing.T) {
	s, _ := scopedServer(t)

	run, code := start(t, s, "repositories=octo-org%2Fapp&mode=extended", oneQuery)
	if code != http.StatusOK || len(run.Members) == 0 {
		t.Fatalf("the fixture's extended scope answered %d with %d members; it must resolve to a member",
			code, len(run.Members))
	}
	code, body := startRefusalWith(t, s, "repositories=octo-org%2Fapp&mode=extended", onePrediction)
	if code != http.StatusBadRequest || !strings.Contains(body, "regular") {
		t.Errorf("an extended prediction answered %d: %s, want 400 naming the regular shape", code, body)
	}
}

// What a prediction would report is listed one member at a time, as a run's results are, and says it
// is a prediction: no finding stands behind any of it.
func TestAPredictionsResultsAreListedAsPredicted(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	run, code := start(t, s, "rules=actions%2F*", onePrediction)
	if code != http.StatusOK || len(run.Members) != 1 {
		t.Fatalf("asking for a prediction answered %d with %d members", code, len(run.Members))
	}
	m := run.Members[0]
	if err := st.RecordPredictedMemberResult(run.RowID, m.DatabaseIdentity, store.MemberResult{
		Status: store.AnalysisSucceeded, Results: 1, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 1},
	}, []store.PredictedResult{{RuleID: "actions/unpinned-tag",
		FilePath: ".github/workflows/ci.yml", StartLine: 9, Message: "Unpinned 3rd party Action"}}); err != nil {
		t.Fatalf("RecordPredictedMemberResult: %v", err)
	}

	got := decode[store.MemberFindings](t, get(t, s,
		"/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/results?repository="+m.Repository))
	if !got.Predicted || !got.Evaluated || len(got.Findings) != 1 || got.Findings[0].StartLine != 9 {
		t.Errorf("the prediction's member reads %+v, want its one predicted result", got)
	}
}
