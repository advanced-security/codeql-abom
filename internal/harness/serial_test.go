package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// wire is one side of a stdio MCP connection a test drives line by line.
type wire struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
}

func (w wire) send(line string) {
	w.t.Helper()
	if _, err := io.WriteString(w.in, line+"\n"); err != nil {
		w.t.Fatal(err)
	}
}

// next is the next line the other side wrote, failing the test after a second of silence.
func (w wire) next() string {
	w.t.Helper()
	select {
	case line := <-w.lines:
		return line
	case <-time.After(time.Second):
		w.t.Fatal("nothing arrived")
		return ""
	}
}

// quiet fails if anything arrives within a moment.
func (w wire) quiet() {
	w.t.Helper()
	select {
	case line := <-w.lines:
		w.t.Fatalf("%s arrived, want nothing yet", line)
	case <-time.After(100 * time.Millisecond):
	}
}

func reading(t *testing.T, r io.Reader) chan string {
	lines := make(chan string, 16)
	go func() {
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 1<<20), 1<<20)
		for s.Scan() {
			lines <- s.Text()
		}
		close(lines)
	}()
	return lines
}

// serialized starts the proxy between a client and a server the test plays both of.
func serialized(t *testing.T) (client, server wire) {
	t.Helper()
	clientIn, toProxy := io.Pipe()
	fromProxy, clientOut := io.Pipe()
	serverIn, toServer := io.Pipe()
	fromServer, serverOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serialize(ctx, clientIn, clientOut, toServer, fromServer) }()
	t.Cleanup(func() {
		cancel()
		toProxy.Close()
		serverOut.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("the proxy did not stop")
		}
	})
	return wire{t: t, in: toProxy, lines: reading(t, fromProxy)},
		wire{t: t, in: serverOut, lines: reading(t, serverIn)}
}

func idOf(t *testing.T, line string) string {
	t.Helper()
	var m struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	return string(m.ID)
}

// ql-mcp's CLI server exits on its first query run, and a command queued behind that one is never
// answered: two PrintAST calls made together left a session waiting twenty minutes. The server is
// handed one tool call at a time, and everything else as it comes.
func TestQlmcpIsHandedOneToolCallAtATime(t *testing.T) {
	client, server := serialized(t)
	client.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"codeql_query_run"}}`)
	client.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"codeql_query_run"}}`)
	if got := idOf(t, server.next()); got != "1" {
		t.Fatalf("the server was handed call %s first, want 1", got)
	}
	server.quiet()

	client.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	client.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if got := idOf(t, server.next()); got != "3" {
		t.Errorf("a listing waited behind a call: got %s", got)
	}
	if got := server.next(); !strings.Contains(got, "notifications/initialized") {
		t.Errorf("a notification waited behind a call: got %s", got)
	}

	// A request from the server shares no ids with the client's, so it frees nothing.
	server.send(`{"jsonrpc":"2.0","id":1,"method":"roots/list"}`)
	if got := client.next(); !strings.Contains(got, "roots/list") {
		t.Fatalf("the client got %s, want the server's request", got)
	}
	server.quiet()

	server.send(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	if got := idOf(t, client.next()); got != "1" {
		t.Errorf("the client got the answer to %s, want 1", got)
	}
	if got := idOf(t, server.next()); got != "2" {
		t.Errorf("after the first answer the server was handed %s, want 2", got)
	}
}

// A call the client gives up on while it waits never reaches the server, and one it gives up on
// while the server works frees the server for the next: a server sends nothing after a cancel.
func TestACancelledCallFreesItsTurn(t *testing.T) {
	client, server := serialized(t)
	for id := 1; id <= 3; id++ {
		client.send(`{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{}}`)
	}
	if got := idOf(t, server.next()); got != "1" {
		t.Fatalf("handed %s first, want 1", got)
	}
	client.send(`{"jsonrpc":"2.0","method":"` + cancelNotice + `","params":{"requestId":2}}`)
	server.quiet()

	client.send(`{"jsonrpc":"2.0","method":"` + cancelNotice + `","params":{"requestId":1}}`)
	if got := server.next(); !strings.Contains(got, cancelNotice) {
		t.Fatalf("the server got %s, want the cancel of the call it was working", got)
	}
	if got := idOf(t, server.next()); got != "3" {
		t.Errorf("after the cancel the server was handed %s, want 3, never the call canceled while waiting", got)
	}
}
