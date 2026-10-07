package figure

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The label model. Labels are monospace, so a line's width is its length, and every box is sized
// here: Graphviz places boxes of exactly this size and never measures text itself.
const (
	nameSize    = 12.0 // the node's name, in px
	contextSize = 10.5 // the line under it
	// advance is a monospace character's width as a fraction of its size, for the widest face the
	// figure's font stack names, so a label never outgrows its box.
	advance    = 0.62
	padX       = 9.0
	glyphSpace = 18.0 // the kind glyph's column, left of both lines
	badgeGap   = 12.0 // between the line under the name and its badges
	badgeDot   = 10.0 // a severity dot and the space after it
	badgeSep   = 8.0  // between two badges
	boxHeight  = 40.0
	maxName    = 40
	maxContext = 40
	// shortSHA is how much of a commit a label shows; the whole one is in its title.
	shortSHA = 7
)

// badge is one fact drawn on a node: its worst finding band, the advisories naming its
// repository, or that it was never analyzed.
type badge struct {
	class string
	text  string
	// dot draws a severity dot before the text, so the band has a channel besides its word.
	dot bool
	// title says in full what the badge abbreviates.
	title string
}

// label is what a node's box says and how big that makes the box, in points.
type label struct {
	name, context string
	badges        []badge
	width, height float64
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// kindWords are the Explorer legend's names for each kind.
var kindWords = map[string]string{
	"workflow":          "workflow",
	"reusable_workflow": "reusable workflow",
	"composite_action":  "composite action",
	"action":            "action",
	"docker_image":      "container image",
}

// KindWord is what the figure calls a kind.
func KindWord(kind string) string {
	if w, ok := kindWords[kind]; ok {
		return w
	}
	return strings.ReplaceAll(kind, "_", " ")
}

func labelOf(n Node) label {
	name, context := nameAndContext(n.GraphNode)
	l := label{name: shorten(name, maxName), context: shorten(context, maxContext), badges: badgesOf(n.GraphNode)}
	contextLine := float64(utf8.RuneCountInString(l.context)) * contextSize * advance
	if len(l.badges) > 0 {
		contextLine += badgeGap
		for i, b := range l.badges {
			if i > 0 {
				contextLine += badgeSep
			}
			if b.dot {
				contextLine += badgeDot
			}
			contextLine += float64(utf8.RuneCountInString(b.text)) * contextSize * advance
		}
	}
	nameLine := float64(utf8.RuneCountInString(l.name)) * nameSize * advance
	l.width = hundredths(padX + glyphSpace + max(nameLine, contextLine) + padX)
	l.height = hundredths(boxHeight)
	return l
}

// hundredths rounds a size up to a hundredth of an inch, the precision Graphviz echoes it at, so
// the box it places and the box drawn are the same size.
func hundredths(points float64) float64 {
	return math.Ceil(points/0.72-1e-9) * 0.72
}

// nameAndContext is a node's own name and the repository it lives in. An action that is a whole
// repository is named by it, and says what it is instead.
func nameAndContext(n store.GraphNode) (string, string) {
	base := BaseKey(n)
	segs := strings.Split(base, "/")
	var name, context string
	switch {
	case n.Kind == "docker_image":
		image := strings.TrimPrefix(base, "docker://")
		name = image[strings.LastIndex(image, "/")+1:]
		context = KindWord(n.Kind)
	case n.Kind == "workflow" || n.Kind == "reusable_workflow" || len(segs) > 2:
		name = segs[len(segs)-1]
		context = repositoryOf(n, segs)
	default:
		name, context = base, KindWord(n.Kind)
	}
	if name == "" {
		name = base
	}
	if n.Ref != "" {
		name += "@" + ShortRef(n.Ref)
	}
	return name, context
}

func repositoryOf(n store.GraphNode, segs []string) string {
	if n.Repository != "" {
		return n.Repository
	}
	if len(segs) > 1 {
		return segs[0] + "/" + segs[1]
	}
	return KindWord(n.Kind)
}

// ShortRef is a reference as a label shows it: a commit cut to its first characters, anything else
// whole. The full reference travels in the title.
func ShortRef(ref string) string {
	if commitSHA.MatchString(ref) {
		return ref[:shortSHA]
	}
	return ref
}

// shorten cuts a line to at most limit characters, keeping its version, which is the evidence.
func shorten(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	keep := ""
	if at := strings.LastIndex(s, "@"); at > 0 && utf8.RuneCountInString(s[at:]) < limit/2 {
		s, keep = s[:at], s[at:]
	}
	runes := []rune(s)
	return string(runes[:limit-1-utf8.RuneCountInString(keep)]) + "…" + keep
}

// bandOrder is code scanning's order, the most severe first; unknown is a rule declaring no score.
var bandOrder = []string{"critical", "high", "medium", "low", "unknown"}

func badgesOf(n store.GraphNode) []badge {
	var out []badge
	if n.Findings > 0 {
		out = append(out, findingsBadge(n))
	}
	if n.Advisories > 0 {
		out = append(out, badge{class: "abom-fig-advisory", text: plural(n.Advisories, "advisory", "advisories"),
			title: plural(n.Advisories, "published advisory names", "published advisories name") +
				" this repository; whether this version is affected is not stated"})
	}
	// Absent is not none: a dependency never analyzed has findings and dependencies nobody read.
	if n.IsExternal && !n.Analyzed {
		out = append(out, badge{class: "abom-fig-unanalyzed-badge", text: "not analyzed",
			title: "never analyzed: what it runs and what it carries are unknown, not none"})
	}
	return out
}

func findingsBadge(n store.GraphNode) badge {
	counts := map[string]int{}
	for _, f := range n.Facets {
		counts[f.Band] += f.Count
	}
	worst, parts := "", []string{}
	for _, band := range bandOrder {
		if counts[band] == 0 {
			continue
		}
		if worst == "" {
			worst = band
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[band], band))
	}
	if worst == "" {
		return badge{class: "abom-fig-findings", text: plural(n.Findings, "finding", "findings"),
			title: plural(n.Findings, "finding", "findings")}
	}
	text := fmt.Sprintf("%d %s", counts[worst], worst)
	if rest := n.Findings - counts[worst]; rest > 0 {
		text += fmt.Sprintf(" +%d", rest)
	}
	return badge{class: "abom-fig-band-" + worst, text: text, dot: true,
		title: plural(n.Findings, "finding", "findings") + ": " + strings.Join(parts, " · ")}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
