package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
)

// Serialize relays a stdio MCP connection between a client and a server, handing the server one
// tool call at a time and every other message as it comes.
//
// ql-mcp 2.27.3 queues CodeQL commands on one long-lived CLI server whose pack cache is a fresh,
// empty directory, so a query run there fails to resolve its packs and the server exits. The
// command in flight is retried as a fresh process; one queued behind it is never answered, so two
// calls made together waited out the client's timeout. One CodeQL evaluation at a time is also
// what the machine-wide lock a session holds assumes.
//
// It returns when the server's output ends, on the first error either way, or when ctx ends. A read
// from clientIn still under way then is not interrupted, since an io.Reader offers no way to: the
// caller owns clientIn, and serialize-mcp exits once this returns.
func Serialize(ctx context.Context, clientIn io.Reader, clientOut io.Writer, serverIn io.Writer,
	serverOut io.Reader) error {
	s := &serial{server: serverIn}
	errs := make(chan error, 2)
	// The client hanging up closes the server's input, and what the server says until its output
	// ends is still relayed.
	go func() {
		err := s.fromClient(bufio.NewReader(clientIn))
		if c, ok := serverIn.(io.Closer); ok {
			_ = c.Close()
		}
		if err != nil {
			errs <- err
		}
	}()
	go func() { errs <- s.fromServer(bufio.NewReader(serverOut), clientOut) }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// cancelNotice is the notification a client sends when it gives up on a request.
const cancelNotice = "notifications/cancelled" //nolint:misspell // the MCP method, spelled as the protocol writes it

type serial struct {
	// mu holds the routing state; wmu orders writes to the server. Neither is held while the
	// other side is written, so a full pipe one way cannot stop the other.
	mu     sync.Mutex
	wmu    sync.Mutex
	server io.Writer
	// busy is the id of the tool call the server is answering, empty while it answers none.
	busy  string
	queue []waiting
}

type waiting struct {
	id   string
	line []byte
}

// message is what the relay reads of a JSON-RPC message; the rest passes through untouched.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		RequestID json.RawMessage `json:"requestId"`
	} `json:"params"`
}

func parse(line []byte) (message, bool) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return message{}, false
	}
	return m, true
}

// key is an id as JSON text without surrounding space, so 1 and "1" stay distinct.
func key(raw json.RawMessage) string {
	return string(bytes.TrimSpace(raw))
}

func (s *serial) fromClient(r *bufio.Reader) error {
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if werr := s.send(s.route(line)...); werr != nil {
				return werr
			}
		}
		if err != nil {
			return eof(err)
		}
	}
}

// route decides what one message from the client sends the server now: itself, nothing while a
// call waits its turn, or a cancel and the next call it frees.
func (s *serial) route(line []byte) [][]byte {
	m, ok := parse(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case ok && m.Method == "tools/call" && len(m.ID) > 0:
		if s.busy != "" {
			s.queue = append(s.queue, waiting{id: key(m.ID), line: line})
			return nil
		}
		s.busy = key(m.ID)
	case ok && m.Method == cancelNotice && len(m.Params.RequestID) > 0:
		id := key(m.Params.RequestID)
		if i := slices.IndexFunc(s.queue, func(w waiting) bool { return w.id == id }); i >= 0 {
			s.queue = slices.Delete(s.queue, i, i+1)
			return nil
		}
		if id == s.busy {
			return append([][]byte{line}, s.release()...)
		}
	}
	return [][]byte{line}
}

// release frees the server and returns the next call waiting, with the relay's state held.
func (s *serial) release() [][]byte {
	s.busy = ""
	if len(s.queue) == 0 {
		return nil
	}
	next := s.queue[0]
	s.queue = s.queue[1:]
	s.busy = next.id
	return [][]byte{next.line}
}

func (s *serial) send(lines ...[]byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for _, line := range lines {
		if _, err := s.server.Write(line); err != nil {
			return err
		}
	}
	return nil
}

func (s *serial) fromServer(r *bufio.Reader, client io.Writer) error {
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if _, werr := client.Write(line); werr != nil {
				return werr
			}
			if werr := s.send(s.answered(line)...); werr != nil {
				return werr
			}
		}
		if err != nil {
			return eof(err)
		}
	}
}

// answered frees the server when a line from it answers the call it was handed, and returns the
// next call waiting. A response carries an id and no method; the server's own requests carry both.
func (s *serial) answered(line []byte) [][]byte {
	m, ok := parse(line)
	if !ok || m.Method != "" || len(m.ID) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key(m.ID) != s.busy {
		return nil
	}
	return s.release()
}

func eof(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
