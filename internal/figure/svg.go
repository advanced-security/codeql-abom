package figure

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Meta is what a figure's caption says about where it came from.
type Meta struct {
	// Terms are the page's terms the figure was cut under, each as a reader reads it.
	Terms []string
	// AsOf is when the graph it was cut from was recorded.
	AsOf string
	// Graphviz is the version that placed it, which its bytes depend on.
	Graphviz string
}

// Figure is one drawing and its size in px.
type Figure struct {
	SVG    []byte
	Width  float64
	Height float64
}

// ErrNothingToDraw reports a closure with no node in it: the key is not in the projection.
var ErrNothingToDraw = errors.New("nothing here is named that, so there is nothing to draw")

const (
	margin     = 12.0
	legendSize = 10.5
	lineHeight = 15.0
	legendGap  = 14.0 // between the drawing and the legend
	minWidth   = 480.0
)

// Draw places a closure and writes its drawing: the same bytes for the same closure, terms and
// Graphviz version.
func Draw(ctx context.Context, c Closure, opts Options, meta Meta) (Figure, error) {
	if len(c.Nodes) == 0 {
		return Figure{}, ErrNothingToDraw
	}
	p, err := Place(ctx, c, opts)
	if err != nil {
		return Figure{}, err
	}
	return render(c, p, opts, meta), nil
}

func render(c Closure, p Placement, opts Options, meta Meta) Figure {
	width := math.Ceil(max(p.width+2*margin, minWidth))
	legend := legendRows(legendItems(c), width-2*margin)
	caption := wrap(captionLines(c, opts, meta), int((width-2*margin)/(legendSize*advance)))
	height := math.Ceil(margin + p.height + legendGap + float64(len(legend))*lineHeight + 4 +
		float64(len(caption))*lineHeight + margin)

	id := figureID(c.Key)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="abom-fig" width="%s" height="%s" `+
		`viewBox="0 0 %s %s" role="group" aria-labelledby="%s-title" aria-describedby="%s-desc">`+"\n",
		num(width), num(height), num(width), num(height), id, id)
	fmt.Fprintf(&b, "<title id=\"%s-title\">What runs %s and what it runs</title>\n", id, esc(c.Key))
	fmt.Fprintf(&b, "<desc id=\"%s-desc\">%s</desc>\n", id, esc(strings.Join(captionLines(c, opts, meta), " ")))
	b.WriteString(style)
	fmt.Fprintf(&b, "<rect class=\"abom-fig-bg\" x=\"0\" y=\"0\" width=\"%s\" height=\"%s\"/>\n", num(width), num(height))
	fmt.Fprintf(&b, "<g transform=\"translate(%s %s)\">\n", num(margin), num(margin))
	drawEdges(&b, c, p)
	drawNodes(&b, c, p)
	b.WriteString("</g>\n")

	y := margin + p.height + legendGap
	for _, row := range legend {
		drawLegendRow(&b, row, margin, y)
		y += lineHeight
	}
	y += 4
	for _, line := range caption {
		fmt.Fprintf(&b, "<text class=\"abom-fig-caption\" x=\"%s\" y=\"%s\">%s</text>\n", num(margin), num(y+8), esc(line))
		y += lineHeight
	}
	b.WriteString("</svg>\n")
	return Figure{SVG: []byte(b.String()), Width: width, Height: height}
}

// style themes the drawing through CSS variables the page sets from its palette. Each falls back to
// the light palette, which is what the file says standing alone and what print uses.
const style = `<style>
.abom-fig{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace}
.abom-fig-bg{fill:var(--abom-fig-bg, #ffffff)}
.abom-fig-edge path{fill:none;stroke:var(--abom-fig-edge, #adb5bd);stroke-width:1.2}
.abom-fig-edge polygon{fill:var(--abom-fig-edge, #adb5bd)}
.abom-fig-route path{stroke:var(--abom-fig-route, #c25100);stroke-width:2}
.abom-fig-route polygon{fill:var(--abom-fig-route, #c25100)}
.abom-fig-box{fill:var(--abom-fig-node-bg, #ffffff);stroke:var(--abom-fig-node-line, #868e96);stroke-width:1}
.abom-fig-root .abom-fig-box{fill:var(--abom-fig-root-bg, #fff4e6);stroke:var(--abom-fig-focus, #b35c00);stroke-width:2}
.abom-fig-unanalyzed .abom-fig-box{stroke-dasharray:4 3}
.abom-fig-stripe{fill:var(--abom-fig-owned, #1971c2)}
.abom-fig-name{font-size:12px;fill:var(--abom-fig-fg, #212529)}
.abom-fig-root .abom-fig-name{font-weight:700}
.abom-fig-context,.abom-fig-badge text{font-size:10.5px;fill:var(--abom-fig-muted, #495057)}
.abom-fig-advisory text{fill:var(--abom-fig-advisory, #6741d9)}
.abom-fig-unanalyzed-badge text{font-style:italic}
.abom-fig-dot{stroke:none}
.abom-fig-band-critical .abom-fig-dot{fill:var(--abom-fig-critical, #c62828)}
.abom-fig-band-high .abom-fig-dot{fill:var(--abom-fig-high, #e65100)}
.abom-fig-band-medium .abom-fig-dot{fill:var(--abom-fig-medium, #f9a825)}
.abom-fig-band-low .abom-fig-dot{fill:var(--abom-fig-low, #1565c0)}
.abom-fig-band-unknown .abom-fig-dot{fill:var(--abom-fig-unknown, #616161)}
.abom-fig-band-critical text,.abom-fig-band-high text,.abom-fig-band-medium text,.abom-fig-band-low text,.abom-fig-band-unknown text{fill:var(--abom-fig-fg, #212529)}
.abom-fig-glyph-workflow{fill:var(--abom-fig-kind-workflow, #0ca678)}
.abom-fig-glyph-reusable_workflow{fill:var(--abom-fig-kind-reusable_workflow, #087f5b)}
.abom-fig-glyph-composite_action{fill:var(--abom-fig-kind-composite_action, #e8590c)}
.abom-fig-glyph-action{fill:var(--abom-fig-kind-action, #c92a2a)}
.abom-fig-glyph-docker_image{fill:var(--abom-fig-kind-docker_image, #9c36b5)}
.abom-fig-glyph-other{fill:var(--abom-fig-muted, #868e96)}
.abom-fig-legend text,.abom-fig-caption{font-size:10.5px;fill:var(--abom-fig-muted, #495057)}
.abom-fig-legend .abom-fig-box{fill:none}
a.abom-fig-node:hover .abom-fig-box,a.abom-fig-node:focus .abom-fig-box{stroke:var(--abom-fig-focus, #b35c00);stroke-width:2.5}
a.abom-fig-node:focus{outline:none}
</style>
`

func drawEdges(b *strings.Builder, c Closure, p Placement) {
	edges := map[[2]string]Edge{}
	for _, e := range c.Edges {
		edges[[2]string{e.Source, e.Target}] = e
	}
	nodes := map[string]Node{}
	for _, n := range c.Nodes {
		nodes[n.Key] = n
	}
	for _, r := range p.routes {
		e, ok := edges[[2]string{r.Tail, r.Head}]
		if !ok {
			continue
		}
		class := "abom-fig-edge"
		if e.Route {
			class += " abom-fig-route"
		}
		from, _ := nameAndContext(nodes[r.Tail].GraphNode)
		to, _ := nameAndContext(nodes[r.Head].GraphNode)
		title := from + " uses " + to
		if nodes[r.Head].Ref == "" && len(e.Refs) > 0 {
			title += " at " + strings.Join(e.Refs, ", ")
		}
		if e.Route {
			title += ", on the way to a finding or an advisory"
		}
		fmt.Fprintf(b, "<g class=\"%s\"><title>%s</title><path d=\"%s\"/>", class, esc(title), splinePath(r.Points))
		if r.Arrow != nil {
			fmt.Fprintf(b, "<polygon points=\"%s\"/>", arrowPoints(r.Points[len(r.Points)-1], *r.Arrow))
		}
		b.WriteString("</g>\n")
	}
}

// splinePath writes Graphviz's B-spline: a start point, then one cubic segment per three points.
func splinePath(points []layout.Point) string {
	var b strings.Builder
	fmt.Fprintf(&b, "M%s %s", num(points[0].X), num(points[0].Y))
	for i := 1; i+2 < len(points); i += 3 {
		fmt.Fprintf(&b, " C%s %s %s %s %s %s", num(points[i].X), num(points[i].Y),
			num(points[i+1].X), num(points[i+1].Y), num(points[i+2].X), num(points[i+2].Y))
	}
	return b.String()
}

// arrowPoints is the arrowhead from where the spline ends to the tip Graphviz left room for.
func arrowPoints(base, tip layout.Point) string {
	dx, dy := tip.X-base.X, tip.Y-base.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return fmt.Sprintf("%s,%s", num(tip.X), num(tip.Y))
	}
	nx, ny := -dy/length*3.5, dx/length*3.5
	return fmt.Sprintf("%s,%s %s,%s %s,%s", num(tip.X), num(tip.Y),
		num(base.X+nx), num(base.Y+ny), num(base.X-nx), num(base.Y-ny))
}

func drawNodes(b *strings.Builder, c Closure, p Placement) {
	for _, n := range c.Nodes {
		bx, l := p.boxes[n.Key], p.labels[n.Key]
		x0, y0 := bx.X-bx.W/2, bx.Y-bx.H/2
		class := "abom-fig-node abom-fig-kind-" + safeClass(n.Kind)
		if n.Side == SideRoot {
			class += " abom-fig-root"
		}
		if n.Owned {
			class += " abom-fig-owned"
		}
		if n.IsExternal && !n.Analyzed {
			class += " abom-fig-unanalyzed"
		}
		fmt.Fprintf(b, "<a href=\"%s\" class=\"%s\" aria-label=\"%s\">", esc(nodeHref(n.GraphNode)), class, esc(spokenName(n)))
		fmt.Fprintf(b, "<title>%s</title>", esc(nodeTitle(n)))
		fmt.Fprintf(b, "<rect class=\"abom-fig-box\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"5\"/>",
			num(x0), num(y0), num(bx.W), num(bx.H))
		if n.Owned {
			fmt.Fprintf(b, "<rect class=\"abom-fig-stripe\" x=\"%s\" y=\"%s\" width=\"4\" height=\"%s\" rx=\"2\"/>",
				num(x0+1), num(y0+1), num(bx.H-2))
		}
		b.WriteString(glyph(n.Kind, x0+padX+1, y0+8))
		tx := x0 + padX + glyphSpace
		fmt.Fprintf(b, "<text class=\"abom-fig-name\" x=\"%s\" y=\"%s\">%s</text>", num(tx), num(y0+17), esc(l.name))
		fmt.Fprintf(b, "<text class=\"abom-fig-context\" x=\"%s\" y=\"%s\">%s</text>", num(tx), num(y0+32), esc(l.context))
		right := x0 + bx.W - padX
		for i := len(l.badges) - 1; i >= 0; i-- {
			bd := l.badges[i]
			textWidth := float64(utf8.RuneCountInString(bd.text)) * contextSize * advance
			fmt.Fprintf(b, "<g class=\"abom-fig-badge %s\"><title>%s</title>", bd.class, esc(bd.title))
			if bd.dot {
				fmt.Fprintf(b, "<circle class=\"abom-fig-dot\" cx=\"%s\" cy=\"%s\" r=\"3.5\"/>",
					num(right-textWidth-6), num(y0+28.5))
			}
			fmt.Fprintf(b, "<text x=\"%s\" y=\"%s\" text-anchor=\"end\">%s</text></g>", num(right), num(y0+32), esc(bd.text))
			right -= textWidth + badgeSep
			if bd.dot {
				right -= badgeDot
			}
		}
		b.WriteString("</a>\n")
	}
}

// glyph is the Explorer legend's shape for a kind, in its 16-unit box scaled to the label.
func glyph(kind string, x, y float64) string {
	class := "abom-fig-glyph-" + kind
	var shape string
	switch kind {
	case "workflow":
		shape = `<circle cx="8" cy="8" r="5.5"/>`
	case "reusable_workflow":
		shape = `<polygon points="8,2 14,8 8,14 2,8"/>`
	case "composite_action":
		shape = `<polygon points="8,2 14,14 2,14"/>`
	case "action":
		shape = `<polygon points="8,2 13.2,5 13.2,11 8,14 2.8,11 2.8,5"/>`
	case "docker_image":
		shape = `<rect x="3.5" y="3" width="9" height="10" rx="4.5"/>`
	default:
		class = "abom-fig-glyph-other"
		shape = `<polygon points="8,4 13,13 3,13"/>`
	}
	return fmt.Sprintf("<g class=\"%s\" transform=\"translate(%s %s) scale(0.75)\">%s</g>", class, num(x), num(y), shape)
}

// nodeHref is a node's page: its key's, and at its version when it stands for one. Each path
// segment is escaped, and a segment of dots is too, so no key can walk the link elsewhere.
func nodeHref(n store.GraphNode) string {
	segs := strings.Split(BaseKey(n), "/")
	for i, s := range segs {
		switch s {
		case ".":
			segs[i] = "%2E"
		case "..":
			segs[i] = "%2E%2E"
		default:
			segs[i] = url.PathEscape(s)
		}
	}
	href := "/node/" + strings.Join(segs, "/")
	if n.Ref != "" {
		href += "?at=" + url.QueryEscape(n.Ref)
	}
	return href
}

// spokenName is a node's accessible name: what it is, whose, and what it carries.
func spokenName(n Node) string {
	parts := []string{BaseKey(n.GraphNode)}
	if n.Ref != "" {
		parts[0] += " at " + n.Ref
	}
	parts = append(parts, KindWord(n.Kind))
	if n.Side == SideRoot {
		parts = append(parts, "this page")
	}
	if n.Owned {
		parts = append(parts, "yours")
	}
	for _, bd := range badgesOf(n.GraphNode) {
		parts = append(parts, bd.title)
	}
	return strings.Join(parts, ", ")
}

// nodeTitle is the whole key and every fact, since a label may have been shortened to fit.
func nodeTitle(n Node) string {
	title := BaseKey(n.GraphNode)
	if n.Ref != "" {
		title += "@" + n.Ref
	}
	facts := []string{KindWord(n.Kind)}
	if n.Owned {
		facts = append(facts, "yours")
	}
	for _, bd := range badgesOf(n.GraphNode) {
		facts = append(facts, bd.title)
	}
	return title + "\n" + strings.Join(facts, " · ")
}

// legendItem is one entry in the legend: what it shows and how wide it is.
type legendItem struct {
	icon  string // drawn at the item's origin, 12 units wide
	words string
}

func (l legendItem) width() float64 {
	return 16 + float64(utf8.RuneCountInString(l.words))*legendSize*advance + 18
}

// legendItems names only what this drawing uses, in the Explorer legend's order.
func legendItems(c Closure) []legendItem {
	present := map[string]bool{}
	owned, unanalyzed, route := false, false, false
	for _, n := range c.Nodes {
		present[n.Kind] = true
		owned = owned || n.Owned
		unanalyzed = unanalyzed || n.IsExternal && !n.Analyzed
	}
	for _, e := range c.Edges {
		route = route || e.Route
	}
	items := []legendItem{{icon: `<rect class="abom-fig-box" x="0" y="-9" width="12" height="10" rx="2" style="stroke:var(--abom-fig-focus, #b35c00);stroke-width:2"/>`,
		words: "this page"}}
	for _, kind := range []string{"workflow", "reusable_workflow", "composite_action", "action", "docker_image"} {
		if present[kind] {
			items = append(items, legendItem{icon: glyph(kind, 0, -10), words: KindWord(kind)})
		}
	}
	if owned {
		items = append(items, legendItem{icon: `<rect class="abom-fig-stripe" x="2" y="-10" width="4" height="12" rx="2"/>`, words: "yours"})
	}
	if route {
		items = append(items, legendItem{icon: `<g class="abom-fig-edge abom-fig-route"><path d="M0 -4 L12 -4"/></g>`,
			words: "leads to a finding or advisory"})
	}
	if unanalyzed {
		items = append(items, legendItem{icon: `<rect class="abom-fig-box" x="0" y="-9" width="12" height="10" rx="2" style="stroke-dasharray:3 2"/>`,
			words: "not analyzed"})
	}
	return items
}

// legendRows wraps the legend to the drawing's width.
func legendRows(items []legendItem, width float64) [][]legendItem {
	var rows [][]legendItem
	var row []legendItem
	used := 0.0
	for _, it := range items {
		if len(row) > 0 && used+it.width() > width {
			rows = append(rows, row)
			row, used = nil, 0
		}
		row = append(row, it)
		used += it.width()
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func drawLegendRow(b *strings.Builder, row []legendItem, x, y float64) {
	b.WriteString("<g class=\"abom-fig-legend\">")
	for _, it := range row {
		fmt.Fprintf(b, "<g transform=\"translate(%s %s)\">%s<text x=\"16\" y=\"0\">%s</text></g>",
			num(x), num(y+8), it.icon, esc(it.words))
		x += it.width()
	}
	b.WriteString("</g>\n")
}

// captionLines say what was drawn and what was not, under which terms, from when, and what placed
// it, in the reader's words.
func captionLines(c Closure, opts Options, meta Meta) []string {
	before, after := "on the left", "on the right"
	if opts.direction() == TopToBottom {
		before, after = "above", "below"
	}
	lines := []string{fmt.Sprintf("%d of %d drawn: what runs %s %s, what it runs %s.",
		c.Counts.Drawn, c.Counts.InClosure, c.Key, before, after)}
	if c.Counts.LeftOut > 0 {
		lines = append(lines, fmt.Sprintf("%d left out, yours and the nearest kept first; the Explorer draws all of it.",
			c.Counts.LeftOut))
	}
	if c.Counts.VersionsDrawn < c.Counts.Versions {
		lines = append(lines, fmt.Sprintf("%d of %d versions in use drawn, the most used first; choose one in Versions in use to draw it alone.",
			c.Counts.VersionsDrawn, c.Counts.Versions))
	}
	switch {
	case c.Counts.NotListed == 1:
		lines = append(lines, "1 more runs it from a repository this page's terms do not list.")
	case c.Counts.NotListed > 1:
		lines = append(lines, fmt.Sprintf("%d more run it from repositories this page's terms do not list.",
			c.Counts.NotListed))
	}
	terms := "none"
	if len(meta.Terms) > 0 {
		terms = strings.Join(meta.Terms, "; ")
	}
	lines = append(lines, "Terms: "+terms+".")
	lines = append(lines, fmt.Sprintf("Recorded %s. Placed by Graphviz %s.", meta.AsOf, meta.Graphviz))
	return lines
}

// wrap breaks each line at spaces so none is longer than limit characters.
func wrap(lines []string, limit int) []string {
	var out []string
	for _, line := range lines {
		current := ""
		for _, word := range strings.Fields(line) {
			if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > limit {
				out = append(out, current)
				current = ""
			}
			if current != "" {
				current += " "
			}
			current += word
		}
		if current != "" {
			out = append(out, current)
		}
	}
	return out
}

// figureID names this figure's title and description, the same for the same key.
func figureID(key string) string {
	h := fnv.New64a()
	h.Write([]byte(key))
	return "abom-fig-" + strconv.FormatUint(h.Sum64(), 16)
}

// safeClass keeps a value from the store to the characters a class name may carry.
func safeClass(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, s)
}

// esc writes text as XML, which escapes for an attribute too.
func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// num is a coordinate at a tenth of a point, the same text for the same value on every machine.
func num(v float64) string {
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	if s == "-0" {
		return "0"
	}
	return s
}
