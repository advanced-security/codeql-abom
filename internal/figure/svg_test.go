package figure

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

var testMeta = Meta{Terms: []string{"Scope: what you own"}, AsOf: "2026-10-06 13:30 UTC", Graphviz: "16.1.0"}

func draw(t *testing.T, c Closure, opts Options) Figure {
	t.Helper()
	requireGraphviz(t)
	f, err := Draw(context.Background(), c, opts, testMeta)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	return f
}

// element is one parsed SVG element: its name, attributes and the text directly inside it.
type element struct {
	name  string
	attrs map[string]string
	text  string
}

// parse reads the drawing as XML, which fails on anything a key could break.
func parse(t *testing.T, svg []byte) []element {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(svg))
	var out []element
	var stack []int
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the drawing is not well-formed XML: %v\n%s", err, svg)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			e := element{name: v.Name.Local, attrs: map[string]string{}}
			for _, a := range v.Attr {
				e.attrs[a.Name.Local] = a.Value
			}
			out = append(out, e)
			stack = append(stack, len(out)-1)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				out[stack[len(stack)-1]].text += string(v)
			}
		}
	}
	return out
}

func TestTheDrawingIsTheSameBytesEveryTime(t *testing.T) {
	first := draw(t, ciClosure(t), Options{})
	second := draw(t, ciClosure(t), Options{})
	if got := bytes.Count(first.SVG, []byte("<a ")); got != len(ciClosure(t).Nodes) {
		t.Fatalf("the drawing holds %d nodes, want %d", got, len(ciClosure(t).Nodes))
	}
	if !bytes.Equal(first.SVG, second.SVG) {
		t.Fatal("one closure drew two different drawings")
	}
}

// Every node is a link to its own page, named for a reader, and a version links to its key's page
// at that version.
func TestEveryNodeIsANamedLinkToItsPage(t *testing.T) {
	c := ciClosure(t)
	els := parse(t, draw(t, c, Options{}).SVG)
	links := map[string]element{}
	for _, e := range els {
		if e.name == "a" {
			links[e.attrs["href"]] = e
		}
	}
	if len(links) != len(c.Nodes) {
		t.Fatalf("%d links for %d nodes", len(links), len(c.Nodes))
	}
	for _, href := range []string{
		"/node/octo/shared/.github/workflows/build.yml",
		"/node/actions/checkout?at=v4",
		"/node/acme/lint?at=v2",
	} {
		a, ok := links[href]
		if !ok {
			t.Errorf("no link to %s among %v", href, slices.Sorted(func(y func(string) bool) {
				for k := range links {
					if !y(k) {
						return
					}
				}
			}))
			continue
		}
		if strings.TrimSpace(a.attrs["aria-label"]) == "" {
			t.Errorf("the link to %s has no accessible name", href)
		}
	}
}

// Keys come from files ABOM did not write: each is escaped as text, as an attribute and as a URL,
// so the drawing stays one well-formed document and every link goes where its key says.
func TestHostileKeysAreEscapedInTheDrawing(t *testing.T) {
	hostile := []string{`o/a"b`, `o/<script>alert(1)</script>`, `o/&amp;`, `o/#frag`, `o/with space`, `o/q?x=1`}
	var uses []string
	for _, k := range hostile {
		uses = append(uses, k+">r/act")
	}
	f := draw(t, Cut(graphOf(nil, uses...), "r/act", CutOptions{}), Options{})
	if bytes.Contains(f.SVG, []byte("<script")) {
		t.Fatal("a key wrote markup into the drawing")
	}
	hrefs := map[string]bool{}
	for _, e := range parse(t, f.SVG) {
		if e.name == "a" {
			u, err := url.Parse(e.attrs["href"])
			if err != nil {
				t.Fatalf("an unparseable link %q: %v", e.attrs["href"], err)
			}
			hrefs[strings.TrimPrefix(u.Path, "/node/")] = true
			if u.RawQuery != "" || u.Fragment != "" {
				t.Errorf("the link %q gained a query or a fragment from its key", e.attrs["href"])
			}
		}
	}
	for _, k := range hostile {
		if !hrefs[k] {
			t.Errorf("no link leads to %q", k)
		}
	}
}

// The drawing says what it held and what it left out, for a reader of the file and for assistive
// tech. It does not paint that, and it does not name what placed it or which side is which: the
// heading already says that, and a footer inside the scrolling canvas is where it was missed.
func TestTheDrawingSaysWhatItLeftOutWithoutPaintingAFooter(t *testing.T) {
	var uses []string
	for i := range 40 {
		uses = append(uses, "c/w"+string(rune('a'+i%26))+string(rune('a'+i/26))+".yml>r/act")
	}
	// A small limit, not the product cap: this is the sentence a truncated drawing says.
	f := draw(t, Cut(graphOf(nil, uses...), "r/act", CutOptions{Limit: 25}), Options{})
	var title, desc string
	for _, e := range parse(t, f.SVG) {
		switch e.name {
		case "title":
			if title == "" {
				title = e.text
			}
		case "desc":
			desc = e.text
		}
		if strings.Contains(e.attrs["class"], "abom-fig-caption") {
			t.Errorf("the drawing paints a caption: %q", e.text)
		}
	}
	if !strings.Contains(title, "r/act") {
		t.Errorf("the title %q does not name the node", title)
	}
	for _, want := range []string{"25 of 41 drawn", "16 left out", "Scope: what you own", "2026-10-06 13:30 UTC"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description leaves out %q: %s", want, desc)
		}
	}
	for _, banned := range []string{"Graphviz", "on the left", "on the right", "above", "below"} {
		if strings.Contains(desc, banned) || strings.Contains(string(f.SVG), ">"+banned) {
			t.Errorf("the drawing says %q, which a reader does not need", banned)
		}
	}
	if strings.Contains(string(f.SVG), "Placed by") {
		t.Error("the drawing still says what placed it")
	}
}

// The figure speaks the reader's words.
func TestTheDrawingSpeaksTheReadersWords(t *testing.T) {
	var text strings.Builder
	for _, e := range parse(t, draw(t, ciClosure(t), Options{}).SVG) {
		if e.name != "style" {
			text.WriteString(strings.ToLower(e.text) + " ")
		}
	}
	if !strings.Contains(text.String(), "build.yml") {
		t.Fatalf("the drawing names nothing, so this proves nothing: %s", text.String())
	}
	for _, word := range []string{"folded", "hops", "reach", "frontier", "corpus", "generation"} {
		if strings.Contains(text.String(), word) {
			t.Errorf("the drawing says %q", word)
		}
	}
}

var badgeText = regexp.MustCompile(`<g class="abom-fig-badge ([^"]+)"><title>[^<]*</title>(?:<circle[^>]*/>)?<text[^>]*>([^<]+)</text></g>`)

// Severity has a channel besides hue: a dot and the band's word. An advisory is a statement about
// the repository, and a dependency never analyzed says so rather than looking clean.
func TestFindingsAdvisoriesAndCoverageAreDrawnAsWords(t *testing.T) {
	svg := draw(t, ciClosure(t), Options{}).SVG
	classes := map[string]bool{}
	for _, e := range parse(t, svg) {
		for _, c := range strings.Fields(e.attrs["class"]) {
			classes[c] = true
		}
	}
	// Read from the badges on the nodes alone: the legend names "not analyzed" too.
	badges := map[string]bool{}
	for _, m := range badgeText.FindAllStringSubmatch(string(svg), -1) {
		badges[m[1]+": "+m[2]] = true
	}
	for _, want := range []string{"abom-fig-band-medium: 1 medium", "abom-fig-advisory: 2 advisories",
		"abom-fig-unanalyzed-badge: not analyzed"} {
		if !badges[want] {
			t.Errorf("no node carries the badge %q among %v", want, badges)
		}
	}
	for _, want := range []string{"abom-fig-band-medium", "abom-fig-dot", "abom-fig-route", "abom-fig-owned", "abom-fig-root"} {
		if !classes[want] {
			t.Errorf("nothing is drawn with class %s", want)
		}
	}
}

// One drawing is the page's, the download's and the print's: themed through CSS variables whose
// fallbacks are the light palette, so the file stands alone.
func TestTheDrawingStandsAloneInTheLightPalette(t *testing.T) {
	svg := string(draw(t, ciClosure(t), Options{}).SVG)
	if !strings.HasPrefix(svg, "<svg ") || !strings.Contains(svg, `xmlns="http://www.w3.org/2000/svg"`) {
		t.Error("the drawing is not a standalone SVG document")
	}
	if !strings.Contains(svg, "var(--abom-fig-bg, #ffffff)") {
		t.Error("the background does not fall back to the light palette")
	}
}

func TestANodeWithoutVersionsLinksToItsPageAlone(t *testing.T) {
	if got := nodeHref(store.GraphNode{Key: "o/r/.github/workflows/ci.yml", Kind: "workflow"}); got != "/node/o/r/.github/workflows/ci.yml" {
		t.Errorf("nodeHref = %q", got)
	}
}
