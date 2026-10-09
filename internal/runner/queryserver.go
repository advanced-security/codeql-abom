package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// QueryServer evaluates queries through one long-lived CodeQL evaluator.
//
// `database run-queries` already amortizes the process across a lane, which is most of the win.
// What it cannot amortize is the process itself: every repository still pays library-path
// resolution, pack resolution and evaluator startup once. A session that outlives the repository
// pays them once for the crawl.
//
// The protocol is JSON-RPC 2.0 with LSP framing, and it is much larger than the cli-server's one
// command in and one answer out: databases are registered, evaluation is a request against a
// registered database, and progress arrives as interleaved notifications. That is why every
// failure degrades to `ErrServerUnavailable` and the caller re-runs the batch as its own process
// — a protocol this size will meet a CLI that has moved, and a slower crawl is a better answer
// than a stopped one.
type QueryServer struct {
	// CodeQLPath is the CLI to invoke. Empty means "codeql" from PATH.
	CodeQLPath string
	// Threads and RAMMB are fixed when the server starts, unlike a per-invocation flag.
	Threads int
	RAMMB   int

	// One evaluation at a time: responses arrive on one stream, so a second request in flight
	// would race for the first one's answer.
	mu sync.Mutex
	serverProcess
	nextID int

	// transport replaces the process for tests.
	transport func(method string, params any) (json.RawMessage, error)
}

// Evaluate runs every query against one database, returning the BQRS each wrote keyed by the
// query path asked for.
//
// The database is deregistered before returning, because the caller deletes it as soon as the
// lane is done and a registered dataset is one the evaluator still holds open.
func (q *QueryServer) Evaluate(
	ctx context.Context, dbPath string, queryPaths []string, outDir string,
) (map[string]string, error) {
	if len(queryPaths) == 0 {
		return map[string]string{}, nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if err := q.start(ctx); err != nil {
		return nil, err
	}
	if err := q.register(dbPath); err != nil {
		return nil, err
	}
	defer q.deregister(dbPath)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	out := make(map[string]string, len(queryPaths))
	for _, query := range queryPaths {
		outputPath := filepath.Join(outDir, resultName(query)+".bqrs")
		if err := q.runOne(dbPath, query, outputPath); err != nil {
			return nil, err
		}
		out[query] = outputPath
	}
	return out, nil
}

// Close shuts the evaluator down. Closing stdin ends it, as it does the cli-server.
func (q *QueryServer) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stop()
	return nil
}

func (q *QueryServer) register(dbPath string) error {
	_, err := q.call("evaluation/registerDatabases", map[string]any{
		"databases": []string{dbPath},
	})
	return err
}

// deregister is best effort: the batch has its results, and a database left registered costs a
// held dataset rather than a wrong answer.
func (q *QueryServer) deregister(dbPath string) {
	_, _ = q.call("evaluation/deregisterDatabases", map[string]any{
		"databases": []string{dbPath},
	})
}

func (q *QueryServer) runOne(dbPath, queryPath, outputPath string) error {
	raw, err := q.call("evaluation/runQuery", map[string]any{
		"db":                      dbPath,
		"queryPath":               queryPath,
		"outputPath":              outputPath,
		"additionalPacks":         []string{},
		"externalInputs":          map[string]string{},
		"singletonExternalInputs": map[string]string{},
		"target":                  map[string]any{"query": map[string]any{}},
	})
	if err != nil {
		return err
	}

	var result struct {
		ResultType int    `json:"resultType"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%w: reading the result of %s: %w", ErrServerUnavailable, queryPath, err)
	}
	// Zero is success. A non-zero type is the evaluator's own verdict on the query, not a
	// transport failure, so it is reported as itself rather than sending the caller to a
	// fallback that would fail the same way.
	if result.ResultType != 0 {
		message := result.Message
		if message == "" {
			message = fmt.Sprintf("result type %d", result.ResultType)
		}
		return fmt.Errorf("evaluating %s: %s", queryPath, message)
	}
	// A success that wrote nothing is a protocol disagreement rather than an empty result set,
	// and the caller cannot tell the two apart from the map alone.
	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("%w: %s reported success and wrote no results", ErrServerUnavailable, queryPath)
	}
	return nil
}

func (q *QueryServer) progressID() int {
	q.nextID++
	return q.nextID
}

// call sends one request and returns its result, skipping the progress notifications the server
// interleaves with responses.
//
// The parameters travel inside `body`, alongside a `progressId` the server uses to label the
// progress it reports. Sending them bare deserializes to a null and the server answers "internal
// error" with a NullPointerException, which says nothing about the shape being wrong.
func (q *QueryServer) call(method string, params any) (json.RawMessage, error) {
	if q.transport != nil {
		return q.transport(method, params)
	}

	q.nextID++
	id := q.nextID
	if err := writeMessage(q.stdin, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method,
		"params": map[string]any{"body": params, "progressId": q.progressID()},
	}); err != nil {
		diagnostic := q.stop()
		return nil, fmt.Errorf("%w: sending %s%s: %w", ErrServerUnavailable, method, diagnostic, err)
	}

	for {
		body, err := readMessage(q.stdout)
		if err != nil {
			diagnostic := q.stop()
			return nil, fmt.Errorf("%w: running %s%s: %w",
				ErrServerUnavailable, method, diagnostic, err)
		}

		var envelope struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			diagnostic := q.stop()
			return nil, fmt.Errorf("%w: reading the answer to %s%s: %w",
				ErrServerUnavailable, method, diagnostic, err)
		}
		// Anything carrying a method is the server talking to us — progress, mostly — rather
		// than answering. Treating one as the answer desynchronizes the stream for good.
		if envelope.Method != "" || envelope.ID == nil || *envelope.ID != id {
			continue
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("%w: %s: %s",
				ErrServerUnavailable, method, envelope.Error.Message)
		}
		return envelope.Result, nil
	}
}

func (q *QueryServer) start(ctx context.Context) error {
	args := []string{"execute", "query-server2"}
	if q.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", q.Threads))
	}
	if q.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", q.RAMMB))
	}

	return q.startProcess(ctx, q.CodeQLPath, args...)
}

// writeMessage frames one JSON-RPC message the way the query server expects: an LSP header, a
// blank line, then the body.
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// readMessage reads one framed message. The length is mandatory: without it there is no way to
// know where the body ends, and guessing would desynchronize every message after it.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("unreadable Content-Length %q: %w", value, err)
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("a message arrived with no Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
