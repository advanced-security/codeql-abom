package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// A hook promises a delivery that something follows: one wired to a loop that cannot enumerate or
// refresh advisories would settle "listing requested" for a listing nothing will run.
func TestTheEventProcessorAsksOnlyWhatTheLoopCanDo(t *testing.T) {
	s := openStore(t)
	idle := &supervisor.Supervisor{Store: s, Queue: &frontier.Queue{Store: s}}
	p := newProcessor(s, idle, nil, nil, io.Discard)
	if p.List != nil || p.ListFully != nil || p.Read != nil || p.Advisories != nil {
		t.Error("a loop that cannot enumerate or refresh advisories was handed asks it will never answer")
	}

	busy := &supervisor.Supervisor{Store: s, Queue: &frontier.Queue{Store: s},
		Enumerate: func(context.Context, supervisor.Ask) (int, error) { return 0, nil },
		Advisor:   func(context.Context) (int, error) { return 0, nil }}
	p = newProcessor(s, busy, nil, nil, io.Discard)
	if p.List == nil || p.ListFully == nil || p.Read == nil || p.Advisories == nil || p.Wake == nil {
		t.Errorf("a loop that can do all of it was handed only some: %+v", p)
	}
}

// Following up an event is work of its own. What the processor reads of GitHub — protected
// branches, a database, a push's dependency comparison — is on the event follow-up meter, and
// enumeration's shows only enumeration.
func TestEventFollowUpReadsAreChargedToTheirOwnPool(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer gh.Close()
	s := openStore(t)
	pools := work.NewTokenPools()
	reader := eventsClient(context.Background(), gh.URL, s, nil, pools)
	loop := &supervisor.Supervisor{Store: s, Queue: &frontier.Queue{Store: s}}
	p := newProcessor(s, loop, reader, nil, io.Discard)
	if p.Branches == nil || p.Databases == nil || p.Setup == nil || p.Dependencies == nil {
		t.Fatalf("the processor was left without a reader: %+v", p)
	}

	ctx := context.Background()
	if _, err := p.Branches(ctx, "acme/service"); err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if _, err := p.Dependencies(ctx, "acme/service", strings.Repeat("a", 40), strings.Repeat("b", 40)); err != nil {
		t.Fatalf("Dependencies: %v", err)
	}
	_, _ = p.Databases(ctx, "acme/service", "actions")

	spent := map[string]int{}
	for _, sp := range pools.Drain() {
		spent[sp.Pool+"/"+sp.Unit] += sp.Requests
	}
	if spent[work.PoolEvents+"/"+work.UnitREST] != 3 || len(spent) != 1 {
		t.Errorf("spent %v, want three REST requests on the event follow-up pool and nothing else", spent)
	}
}
