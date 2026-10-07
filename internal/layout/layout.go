// Package layout turns DOT into stable Graphviz coordinates.
package layout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Engine is a Graphviz layout program.
type Engine string

// The engines this may run. The closed set keeps caller text from becoming an executable name.
const (
	Dot   Engine = "dot"
	Neato Engine = "neato"
	Fdp   Engine = "fdp"
	Sfdp  Engine = "sfdp"
	Twopi Engine = "twopi"
	Circo Engine = "circo"
	Osage Engine = "osage"
)

// Engines lists what may be asked for.
func Engines() []Engine {
	return []Engine{Dot, Neato, Fdp, Sfdp, Twopi, Circo, Osage}
}

// programFor names the program an engine runs, or reports that nothing goes by that name.
//
// Returning this package's constant ensures exec receives a value from Engines, not caller text.
func programFor(e Engine) (string, bool) {
	for _, known := range Engines() {
		if e == known {
			return string(known), true
		}
	}
	return "", false
}

// Default is the engine used when a caller names none.
//
// A wide canvas is preferable to overlapped nodes or crossing group boxes: positions can be
// scaled and panned, but ambiguous containment is unreadable at any zoom.
var Default = Dot

// clustering is the set that lays a cluster out as a box rather than ignoring the subgraph.
//
// An engine that cannot draw clusters is not a failure, but it must be reported so grouped data
// does not look ungrouped.
var clustering = map[Engine]bool{Dot: true, Neato: true, Fdp: true, Osage: true}

// SupportsClusters reports whether an engine draws a boundary around a grouped subgraph.
func SupportsClusters(e Engine) bool { return clustering[e] }

// errUnavailable distinguishes a missing optional engine from a Graphviz failure.
var errUnavailable = errors.New("layout engine is not installed")

// ErrUnavailable reports whether the engine was missing rather than unhappy.
func ErrUnavailable(err error) bool { return errors.Is(err, errUnavailable) }

// Options selects the engine and bounds what it may cost.
type Options struct {
	Engine Engine
	// Timeout bounds one layout. Zero is DefaultTimeout.
	Timeout time.Duration
	// MaxOutput caps what will be read back. Zero is DefaultMaxOutput.
	MaxOutput int
	// binary overrides the program name, so a test can stand in a machine with no Graphviz.
	binary string
}

// Defaults bound optional layout work before it outlives the request or browser budget.
const (
	DefaultTimeout   = 10 * time.Second
	DefaultMaxOutput = 64 << 20
)

// Position is where an engine placed a node, in Graphviz points with the origin bottom left.
type Position struct {
	Key string  `json:"key"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
}

// Cluster is the boundary an engine drew around a group.
type Cluster struct {
	Name string  `json:"name"`
	X1   float64 `json:"x1"`
	Y1   float64 `json:"y1"`
	X2   float64 `json:"x2"`
	Y2   float64 `json:"y2"`
}

// Result is one computed layout.
type Result struct {
	Engine    string     `json:"engine"`
	Width     float64    `json:"width"`
	Height    float64    `json:"height"`
	Positions []Position `json:"positions"`
	Clusters  []Cluster  `json:"clusters"`
	Boxes     []Box      `json:"-"`
	Splines   []Spline   `json:"-"`
}

// Compute lays out a DOT document and returns where everything went.
func Compute(ctx context.Context, document []byte, opts Options) (Result, error) {
	program, known := programFor(opts.Engine)
	if !known {
		return Result{}, fmt.Errorf("%q is not a layout engine; use one of %v",
			opts.Engine, Engines())
	}
	if opts.binary != "" {
		program = opts.binary
	}
	if _, err := exec.LookPath(program); err != nil {
		return Result{}, fmt.Errorf("%w: %s", errUnavailable, program)
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	max := opts.MaxOutput
	if max == 0 {
		max = DefaultMaxOutput
	}

	// Fixed argv and stdin avoid filename quoting and races. `-Gstart=1` makes force engines
	// deterministic.
	cmd := exec.CommandContext(ctx, program, "-Tjson", "-Gstart=1")
	cmd.Stdin = bytes.NewReader(document)
	var out, stderr bytes.Buffer
	cmd.Stdout = &capped{to: &out, max: max}
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("%s layout abandoned: %w", opts.Engine, ctx.Err())
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return Result{}, fmt.Errorf("%s: %w: %s", opts.Engine, err, msg)
		}
		return Result{}, fmt.Errorf("%s: %w", opts.Engine, err)
	}

	result, err := parse(out.Bytes())
	if err != nil {
		return Result{}, fmt.Errorf("reading %s output: %w", opts.Engine, err)
	}
	result.Engine = string(opts.Engine)
	return result, nil
}

// gvOutput is the part of Graphviz's JSON this reads. An edge names its ends by their index in
// the objects list, where clusters come before nodes.
type gvOutput struct {
	BB      string `json:"bb"`
	Objects []struct {
		Name   string `json:"name"`
		Pos    string `json:"pos"`
		BB     string `json:"bb"`
		Width  string `json:"width"`
		Height string `json:"height"`
	} `json:"objects"`
	Edges []struct {
		Tail int    `json:"tail"`
		Head int    `json:"head"`
		Pos  string `json:"pos"`
	} `json:"edges"`
}

// pointsPerInch converts the sizes Graphviz states in inches to the points it places in.
const pointsPerInch = 72

func parse(b []byte) (Result, error) {
	var raw gvOutput
	if err := json.Unmarshal(b, &raw); err != nil {
		return Result{}, err
	}

	var result Result
	if box, ok := rect(raw.BB); ok {
		result.Width, result.Height = box[2]-box[0], box[3]-box[1]
	}
	for _, obj := range raw.Objects {
		// Cluster is a naming convention rather than a field: Graphviz treats a subgraph whose
		// name begins with `cluster` as one, and reports its box the same way.
		if strings.HasPrefix(obj.Name, "cluster") {
			if box, ok := rect(obj.BB); ok {
				result.Clusters = append(result.Clusters, Cluster{
					Name: obj.Name, X1: box[0], Y1: box[1], X2: box[2], Y2: box[3],
				})
			}
			continue
		}
		if x, y, ok := point(obj.Pos); ok {
			result.Positions = append(result.Positions, Position{Key: obj.Name, X: x, Y: y})
			w, errW := strconv.ParseFloat(obj.Width, 64)
			h, errH := strconv.ParseFloat(obj.Height, 64)
			if errW == nil && errH == nil {
				result.Boxes = append(result.Boxes, Box{Key: obj.Name, X: x, Y: y,
					W: w * pointsPerInch, H: h * pointsPerInch})
			}
		}
	}
	for _, e := range raw.Edges {
		if e.Tail < 0 || e.Tail >= len(raw.Objects) || e.Head < 0 || e.Head >= len(raw.Objects) {
			return Result{}, fmt.Errorf("an edge names object %d or %d of %d", e.Tail, e.Head, len(raw.Objects))
		}
		spline, ok := splineOf(e.Pos)
		if !ok {
			continue
		}
		spline.Tail, spline.Head = raw.Objects[e.Tail].Name, raw.Objects[e.Head].Name
		result.Splines = append(result.Splines, spline)
	}
	return result, nil
}

// splineOf reads an edge's pos: an optional `s,x,y` and `e,x,y` naming the arrow tips, then the
// B-spline's control points. A route with more than one spline keeps the first.
func splineOf(pos string) (Spline, bool) {
	var out Spline
	first, _, _ := strings.Cut(pos, ";")
	for _, field := range strings.Fields(first) {
		switch {
		case strings.HasPrefix(field, "e,"):
			if x, y, ok := point(field[2:]); ok {
				out.Arrow = &Point{X: x, Y: y}
			}
		case strings.HasPrefix(field, "s,"):
		default:
			x, y, ok := point(field)
			if !ok {
				return Spline{}, false
			}
			out.Points = append(out.Points, Point{X: x, Y: y})
		}
	}
	return out, len(out.Points) >= 4 && (len(out.Points)-1)%3 == 0
}

func point(s string) (x, y float64, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	x, errX := strconv.ParseFloat(parts[0], 64)
	y, errY := strconv.ParseFloat(parts[1], 64)
	return x, y, errX == nil && errY == nil
}

func rect(s string) ([4]float64, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return [4]float64{}, false
	}
	var box [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return [4]float64{}, false
		}
		box[i] = v
	}
	return box, true
}

// capped stops a runaway engine filling memory, and says so rather than truncating quietly.
type capped struct {
	to      *bytes.Buffer
	max     int
	written int
}

func (c *capped) Write(p []byte) (int, error) {
	if c.written+len(p) > c.max {
		return 0, fmt.Errorf("layout output exceeded %d bytes", c.max)
	}
	c.written += len(p)
	return c.to.Write(p)
}

// Remedy is what to do about a server with no Graphviz, which is a broken installation.
const Remedy = "install Graphviz so that dot is on the server's PATH (brew install graphviz, " +
	"or apt-get install graphviz), then start ABOM again"

// Box is where an engine placed a node and how big it is, in points: X and Y are the center.
type Box struct {
	Key        string
	X, Y, W, H float64
}

// Point is one position in Graphviz points, the origin bottom left.
type Point struct{ X, Y float64 }

// Spline is the route an engine gave one edge: a cubic B-spline of 3n+1 points from tail to head,
// and the tip of the arrow at its head, nil when the edge draws none.
type Spline struct {
	Tail, Head string
	Points     []Point
	Arrow      *Point
}

// versionTimeout bounds asking Graphviz its version, which answers at once when it is installed.
const versionTimeout = 5 * time.Second

// Version reports the installed Graphviz version, as `dot -V` states it.
func Version(ctx context.Context) (string, error) {
	program, _ := programFor(Dot)
	if _, err := exec.LookPath(program); err != nil {
		return "", fmt.Errorf("%w: %s", errUnavailable, program)
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	// `dot -V` writes to stderr, and some builds to stdout.
	out, err := exec.CommandContext(ctx, program, "-V").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("asking %s its version: %w", program, err)
	}
	return parseVersion(string(out))
}

var versionLine = regexp.MustCompile(`(?i)graphviz version (\d+(?:\.\d+)+)`)

func parseVersion(s string) (string, error) {
	m := versionLine.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("no Graphviz version in %q", strings.TrimSpace(s))
	}
	return m[1], nil
}

// Require reports the installed Graphviz version, or why there is none and what to do about it.
func Require(ctx context.Context) (string, error) {
	version, err := Version(ctx)
	if err != nil {
		return "", fmt.Errorf("this server needs Graphviz and cannot run it (%w); %s", err, Remedy)
	}
	return version, nil
}
