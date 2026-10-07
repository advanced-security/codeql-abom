package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type checkState struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	BuiltBy string `json:"builtBy"`
	LastRun *struct {
		Build string `json:"build"`
		Parts []struct {
			Disagreed *int `json:"disagreed"`
		} `json:"parts"`
	} `json:"lastRun"`
}

type checksBody struct {
	Checks []checkState `json:"checks"`
	Trend  []struct {
		Check   int    `json:"check"`
		Build   string `json:"build"`
		Started int    `json:"started"`
	} `json:"trend"`
	Queue struct {
		Items []struct {
			Subject string `json:"subject"`
		} `json:"items"`
		Total int `json:"total"`
	} `json:"queue"`
}

func assertCheckSaysIfItRuns(t *testing.T, want int, c checkState) {
	t.Helper()
	if c.Number != want {
		t.Errorf("check %d is listed as %d; the plan's order is the reader's", want, c.Number)
	}
	built := c.Number >= 7 && c.Number <= 9
	switch {
	case built && (c.State != "built" || c.BuiltBy != ""):
		t.Errorf("check %d reads %q built by %q, want built", c.Number, c.State, c.BuiltBy)
	case !built && (c.State != "not built" || c.BuiltBy == "" || strings.Contains(c.BuiltBy, "item ")):
		t.Errorf("check %d reads %q built by %q, want not built and the title of the plan item that builds it",
			c.Number, c.State, c.BuiltBy)
	case !built && c.LastRun != nil:
		t.Errorf("check %d is not built and reports a run", c.Number)
	}
}

// Zero disagreements from a check that never ran reads like health, so every check says if it runs.
func TestTheChecksEndpointListsEveryCheckAndNamesTheOnesNotBuilt(t *testing.T) {
	srv, s := newServer(t)
	if _, err := s.RecordCheckRun(store.CheckRun{Check: 7, Build: "b1", StartedAt: "2026-10-03",
		Parts: []store.CheckPart{{Part: "abom_node_reach", Compared: 3,
			Disagreements: []store.Disagreement{{Subject: "actions/cache",
				Reading: "abom_node_reach", Class: "derivation"}}}}}); err != nil {
		t.Fatal(err)
	}

	body := decode[checksBody](t, get(t, srv, "/api/checks"))

	if len(body.Checks) != 11 {
		t.Fatalf("listed %d checks, want the plan's 11", len(body.Checks))
	}
	for i, c := range body.Checks {
		assertCheckSaysIfItRuns(t, i+1, c)
	}
	if run := body.Checks[6].LastRun; run == nil || run.Build != "b1" || len(run.Parts) != 1 ||
		run.Parts[0].Disagreed == nil || *run.Parts[0].Disagreed != 1 {
		t.Errorf("check 7's last run reads %+v, want b1's with its one disagreement", run)
	}
	if body.Checks[7].LastRun != nil {
		t.Errorf("check 8 never ran and reports %+v", body.Checks[7].LastRun)
	}
	if len(body.Trend) != 1 || body.Trend[0].Check != 7 || body.Trend[0].Build != "b1" ||
		body.Trend[0].Started != 1 {
		t.Errorf("the trend reads %+v, want check 7 under b1 starting one disagreement", body.Trend)
	}
	if body.Queue.Total != 1 || len(body.Queue.Items) != 1 || body.Queue.Items[0].Subject != "actions/cache" {
		t.Errorf("the queue reads %+v, want the one open disagreement", body.Queue)
	}
}

// The queue pages like every other list, and a term the endpoint does not apply is refused by name.
func TestTheChecksQueuePagesAndRefusesWhatItDoesNotApply(t *testing.T) {
	srv, s := newServer(t)
	if _, err := s.RecordCheckRun(store.CheckRun{Check: 7, Build: "b1", StartedAt: "2026-10-03",
		Parts: []store.CheckPart{{Part: "abom_node_reach", Compared: 3,
			Disagreements: []store.Disagreement{{Subject: "actions/cache",
				Reading: "abom_node_reach", Class: "derivation"}}}}}); err != nil {
		t.Fatal(err)
	}
	if paged := get(t, srv, "/api/checks?offset=1&top=5"); paged.Code != http.StatusOK ||
		!strings.Contains(paged.Body.String(), `"items":[]`) || !strings.Contains(paged.Body.String(), `"total":1`) {
		t.Errorf("the second page reads %d %s, want no items of a total of 1", paged.Code, paged.Body)
	}
	if refused := get(t, srv, "/api/checks?check=7"); refused.Code != http.StatusBadRequest {
		t.Errorf("an unknown parameter answered %d, want it refused by name", refused.Code)
	}
}

// checkFrames reads which checks carry a frame, by number, as the response wrote them.
func checkFrames(t *testing.T, srv *Server) map[int]json.RawMessage {
	t.Helper()
	body := decode[struct {
		Checks []map[string]json.RawMessage `json:"checks"`
	}](t, get(t, srv, "/api/checks"))
	frames := map[int]json.RawMessage{}
	for _, c := range body.Checks {
		var number int
		if err := json.Unmarshal(c["number"], &number); err != nil {
			t.Fatalf("a check without a number: %v", c)
		}
		if frame, ok := c["frame"]; ok {
			frames[number] = frame
		}
	}
	return frames
}

// Check 5 is not built yet, so the runs it will sample are shown beside it rather than nowhere.
func TestCheckFiveCarriesTheRunsItWillSampleAndNoOtherCheckDoes(t *testing.T) {
	srv, s := newServer(t)

	fresh := checkFrames(t, srv)
	for number, frame := range fresh {
		if number != 5 {
			t.Errorf("check %d carries the frame %s, want only check 5", number, frame)
		}
	}
	if string(fresh[5]) != `{"runs":0,"repositories":0}` {
		t.Errorf("check 5 on a fresh store carries %s, want no runs and no newest", fresh[5])
	}

	completed := time.Date(2026, 10, 3, 9, 30, 0, 0, time.UTC)
	for _, r := range []store.WorkflowRun{
		{Repository: "acme/service", RunID: 1, CompletedAt: completed.Add(-time.Hour)},
		{Repository: "acme/service", RunID: 2, CompletedAt: completed},
		{Repository: "acme/library", RunID: 3},
	} {
		if _, err := s.RecordWorkflowRun(r); err != nil {
			t.Fatal(err)
		}
	}
	busy := checkFrames(t, srv)
	if len(busy) != 1 {
		t.Errorf("%d checks carry a frame, want only check 5", len(busy))
	}
	var frame store.WorkflowRunFrame
	if err := json.Unmarshal(busy[5], &frame); err != nil {
		t.Fatalf("check 5's frame %s: %v", busy[5], err)
	}
	if frame.Runs != 3 || frame.Repositories != 2 || frame.Newest == nil || !frame.Newest.Equal(completed) {
		t.Errorf("check 5's frame reads %s, want 3 runs in 2 repositories, the newest completed %v",
			busy[5], completed)
	}
}
