// Package layout turns a DOT document into coordinates.
//
// Graphviz does the placing. This owns which engines may run, that the same document places the
// same way twice, and that a request which cannot be finished is abandoned rather than waited on.
//
// The interface keeps its own renderer: what arrives here is where things go, not a picture.
package layout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Engine is a Graphviz layout program.
type Engine string

// The engines this may run. An engine name selects a program to execute, so the set is closed:
// anything outside it is refused before a process is started rather than passed through.
const (
	// Dot ranks a directed graph, which reads a closure as distance from what you own. That
	// ranking is also what makes it wrong for a screen: a broad rank of siblings spreads sideways
	// without bound, so it suits a printed hierarchy rather than a canvas.
	Dot   Engine = "dot"
	Neato Engine = "neato"
	Fdp   Engine = "fdp"
	Sfdp  Engine = "sfdp"
	Twopi Engine = "twopi"
	Circo Engine = "circo"
	Osage Engine = "osage"
)

var engines = map[Engine]bool{
	Dot: true, Neato: true, Fdp: true, Sfdp: true, Twopi: true, Circo: true, Osage: true,
}

// Engines lists what may be asked for, so a caller can offer the set rather than guess at it.
func Engines() []Engine {
	return []Engine{Dot, Neato, Fdp, Sfdp, Twopi, Circo, Osage}
}

// Default is the engine used when a caller names none.
//
// Measured against Graphviz 16.1.0 on a corpus shape -- repositories fanning into a shared pool
// of actions -- and the compact answer is the wrong one. Asked to place it, neato returned a
// canvas near a screen's proportions and dot returned a ribbon nine times wider than it was
// tall, which is why dot looks like the worse choice on the numbers alone. It is not: neato
// bought those proportions by stacking 75 pairs of nodes on top of each other, and with overlap
// removal turned on to fix that, all 21 pairs of its group boxes crossed instead. dot overlapped
// no nodes and crossed no boundaries.
//
// A wide canvas is a cost the interface can pay, because positions are scaled to fit and the
// canvas pans and zooms. Nodes stacked on nodes, and a group box drawn across another group box,
// are not readable at any zoom. So the default is the engine that keeps things apart, and a
// caller who wants a squarer picture asks for one by name.
var Default = Dot

// clustering is the set that lays a cluster out as a box rather than ignoring the subgraph.
//
// Measured against Graphviz 16.1.0 rather than read from documentation: asked for two clusters,
// dot, neato, fdp and osage each returned two boxes and sfdp, twopi and circo returned none. An
// engine that cannot do it is not a failure, but a reader who grouped and saw no boundaries with
// nothing said would take it for a corpus where nothing shared a group.
var clustering = map[Engine]bool{Dot: true, Neato: true, Fdp: true, Osage: true}

// SupportsClusters reports whether an engine draws a boundary around a grouped subgraph.
func SupportsClusters(e Engine) bool { return clustering[e] }

// errUnavailable marks an engine that is not installed, which is a different answer from one that
// ran and failed: the first is a machine without Graphviz and the interface degrades, the second
// is a document Graphviz would not accept and somebody has to look at it.
var errUnavailable = errors.New("layout engine is not installed")

// ErrUnavailable reports whether the engine was missing rather than unhappy.
func ErrUnavailable(err error) bool { return errors.Is(err, errUnavailable) }

// Available reports whether an engine can be run at all.
func Available(e Engine) bool {
	if !engines[e] {
		return false
	}
	_, err := exec.LookPath(string(e))
	return err == nil
}

// Options selects the engine and bounds what it may cost.
type Options struct {
	Engine Engine
	// Timeout bounds one layout. Zero is DefaultTimeout. A pathological graph otherwise holds a
	// connection until somebody restarts the server.
	Timeout time.Duration
	// MaxOutput caps what will be read back. Zero is DefaultMaxOutput.
	MaxOutput int
	// binary overrides the program name, so a test can stand in a machine with no Graphviz.
	binary string
}

// Defaults chosen against the seeded corpus: a layout that has not finished in ten seconds is not
// one anybody is still waiting for, and a document larger than the cap is past what a browser
// would draw regardless.
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

// Cluster is the box an engine drew around a group. It is the boundary itself rather than a hull
// computed from member positions afterwards, which is the whole reason for laying out here.
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
}

// Compute lays out a DOT document and returns where everything went.
func Compute(ctx context.Context, document []byte, opts Options) (Result, error) {
	if !engines[opts.Engine] {
		return Result{}, fmt.Errorf("%q is not a layout engine; use one of %v",
			opts.Engine, Engines())
	}
	program := opts.binary
	if program == "" {
		program = string(opts.Engine)
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

	// Fixed argv, and the document arrives on stdin rather than as a path: there is then no
	// filename to quote, no temporary file to clean up and nothing for anything else on the
	// machine to swap between writing it and reading it.
	//
	// `-Gstart=1` seeds the force engines. Without it neato, fdp and sfdp seed themselves and
	// the same document draws a different picture every run, which is what this exists to stop.
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

// gvOutput is the part of Graphviz's JSON this reads. Nodes carry a position, clusters carry a
// bounding box, and everything else in the document is somebody else's business.
type gvOutput struct {
	BB      string `json:"bb"`
	Objects []struct {
		Name string `json:"name"`
		Pos  string `json:"pos"`
		BB   string `json:"bb"`
	} `json:"objects"`
}

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
		}
	}
	return result, nil
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
