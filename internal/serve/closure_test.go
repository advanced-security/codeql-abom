package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/figure"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// figureServer is seedGraph and a second starting point running the same action, which nothing
// declared: octo-org is declared, other-org is not.
func figureServer(t *testing.T) *Server {
	t.Helper()
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	repo, err := s.UpsertRepository("other-org/svc", "")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout", IsExternal: true},
		{Key: "other-org/svc/.github/workflows/build.yml", Kind: "workflow", RepositoryID: repo,
			Path: ".github/workflows/build.yml"},
		{Key: "step:svc:0", Kind: "step", RepositoryID: repo,
			ContainerKey: "other-org/svc/.github/workflows/build.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(store.Edge{Key: "e2", SrcKey: "step:svc:0", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", RepositoryID: repo,
		FilePath: ".github/workflows/build.yml", StartLine: 3}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeriveContainment(); err != nil {
		t.Fatal(err)
	}
	return srv
}

// NodeFigure as the interface reads it.
type nodeFigure struct {
	Key       string        `json:"key"`
	SVG       string        `json:"svg"`
	Width     float64       `json:"width"`
	Height    float64       `json:"height"`
	Counts    figure.Counts `json:"counts"`
	Terms     []string      `json:"terms"`
	Caption   []string      `json:"caption"`
	Direction string        `json:"direction"`
}

// A details page draws what runs its node and what it runs.
func TestAFigureDrawsWhatRunsANode(t *testing.T) {
	srv := figureServer(t)
	f := decode[nodeFigure](t, get(t, srv, "/api/node/actions/checkout/-/closure"))
	if !strings.HasPrefix(f.SVG, "<svg ") {
		t.Fatalf("no drawing: %.200s", f.SVG)
	}
	for _, href := range []string{`href="/node/octo-org/app/.github/workflows/ci.yml"`,
		`href="/node/other-org/svc/.github/workflows/build.yml"`} {
		if !strings.Contains(f.SVG, href) {
			t.Errorf("the drawing has no link %s", href)
		}
	}
	if f.Counts.Drawn != 3 || f.Direction != "LR" {
		t.Errorf("counts %+v drawn %s, want the action and both workflows left to right", f.Counts, f.Direction)
	}
}

// What runs it is drawn as What uses: this lists it, under the same terms: under scope=owned the
// repository nobody declared is counted, not drawn, and the caption names the term.
func TestAFiguresCallersAreWhatTheUsesTableLists(t *testing.T) {
	srv := figureServer(t)
	f := decode[nodeFigure](t, get(t, srv, "/api/node/actions/checkout/-/closure?scope=owned"))
	if strings.Contains(f.SVG, "other-org/svc") {
		t.Error("scope=owned drew a repository nobody declared")
	}
	if !strings.Contains(f.SVG, `href="/node/octo-org/app/.github/workflows/ci.yml"`) {
		t.Error("scope=owned left out the declared repository's workflow")
	}
	if f.Counts.NotListed != 1 || len(f.Terms) != 1 || !strings.Contains(f.Terms[0], "what you own") {
		t.Errorf("counts %+v and terms %v, want one not listed and the scope named", f.Counts, f.Terms)
	}
	if !strings.Contains(f.SVG, "1 more runs it") || strings.Contains(f.SVG, "Graphviz") || strings.Contains(f.SVG, "on the left") {
		t.Errorf("the drawing should say who it left out, and not what placed it or which side is which")
	}
	joined := strings.Join(f.Caption, " ")
	if !strings.Contains(joined, "1 more runs it") || !strings.Contains(joined, "what you own") || strings.Contains(joined, "Graphviz") {
		t.Errorf("caption %v, want who was left out and the term, and not what placed the drawing", f.Caption)
	}
}

// A term the page cannot apply is refused by name, as the page's own table refuses it.
func TestAFigureRefusesATermThePageCannotApply(t *testing.T) {
	rec := get(t, figureServer(t), "/api/node/actions/checkout/-/closure?kinds=action")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "kinds") {
		t.Errorf("status %d: %s; want the term refused by name", rec.Code, rec.Body.String())
	}
}

func TestAFigureOfNothingIsNotFound(t *testing.T) {
	rec := get(t, figureServer(t), "/api/node/nobody/nothing/-/closure")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// A server that cannot run Graphviz answers 503 and the remedy, never a figure nobody placed.
func TestAFigureAnswersUnavailableWithTheRemedyWhenGraphvizIsGone(t *testing.T) {
	srv := figureServer(t)
	t.Setenv("PATH", t.TempDir())
	rec := get(t, srv, "/api/node/actions/checkout/-/closure")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "install Graphviz") {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// The download is the same drawing as a file of its own.
func TestTheFigureDownloadsAsTheSameDrawing(t *testing.T) {
	srv := figureServer(t)
	f := decode[nodeFigure](t, get(t, srv, "/api/node/actions/checkout/-/closure?direction=TB"))
	rec := get(t, srv, "/api/node/actions/checkout/-/closure.svg?direction=TB")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, ".svg") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rec.Body.String() != f.SVG {
		t.Error("the download is not the drawing the page shows")
	}
}

// One figure is the same bytes on every request, so a page and its print agree.
func TestAFigureIsTheSameBytesOnEveryRequest(t *testing.T) {
	srv := figureServer(t)
	first := get(t, srv, "/api/node/actions/checkout/-/closure.svg")
	second := get(t, srv, "/api/node/actions/checkout/-/closure.svg")
	if first.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Errorf("two requests drew two figures (%d)", first.Code)
	}
}

func TestAFigureRefusesADirectionItDoesNotDraw(t *testing.T) {
	rec := get(t, figureServer(t), "/api/node/actions/checkout/-/closure?direction=sideways")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "LR") {
		t.Errorf("the refusal does not name what it draws: %v", body)
	}
}
