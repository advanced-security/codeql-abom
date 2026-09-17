package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// The protocol is a JSON array of argv strings followed by a NUL, and a response terminated by a
// NUL. Getting the terminator wrong desynchronizes the stream rather than failing, so every
// subsequent command would read the previous command's tail.
func TestACommandIsWrittenAsJSONArgvTerminatedByNUL(t *testing.T) {
	var buf bytes.Buffer

	if err := writeCommand(&buf, []string{"resolve", "metadata", "a.ql"}); err != nil {
		t.Fatalf("writeCommand: %v", err)
	}

	written := buf.Bytes()
	if written[len(written)-1] != 0 {
		t.Fatalf("command is not NUL-terminated: %q", written)
	}
	var args []string
	if err := json.Unmarshal(written[:len(written)-1], &args); err != nil {
		t.Fatalf("command is not a JSON array: %v (%q)", err, written)
	}
	if strings.Join(args, " ") != "resolve metadata a.ql" {
		t.Errorf("wrote %v, want the argv it was given", args)
	}
}

func TestAResponseEndsAtTheFirstNUL(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("first\x00second\x00"))

	for _, want := range []string{"first", "second"} {
		got, err := readResponse(r)
		if err != nil {
			t.Fatalf("readResponse: %v", err)
		}
		if string(got) != want {
			t.Errorf("read %q, want %q", got, want)
		}
	}
}

// A CLI that dies mid-answer must report that, not hand back a truncated result that reads like
// a successful empty response.
func TestAResponseWithNoTerminatorIsAnError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("cut off"))

	if _, err := readResponse(r); err == nil {
		t.Fatal("a truncated response was accepted")
	}
}

// fakeCLI records what reached the server, so the framing and the serialization can be exercised
// without a JVM.
type fakeCLI struct {
	mu    sync.Mutex
	calls [][]string
	// overlapped records whether two commands were ever in flight together, which the protocol
	// cannot survive: one stream, one NUL-delimited answer at a time.
	inFlight   int
	overlapped bool
}

func TestOneCommandAtATimeReachesTheServer(t *testing.T) {
	f := &fakeCLI{}
	c := &CLIServer{transport: func(args []string) ([]byte, error) {
		f.mu.Lock()
		f.inFlight++
		if f.inFlight > 1 {
			f.overlapped = true
		}
		f.calls = append(f.calls, args)
		f.mu.Unlock()

		defer func() {
			f.mu.Lock()
			f.inFlight--
			f.mu.Unlock()
		}()
		return []byte("ok"), nil
	}}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Run(context.Background(), "resolve", "metadata", "a.ql"); err != nil {
				t.Errorf("Run: %v", err)
			}
		}()
	}
	wg.Wait()

	if f.overlapped {
		t.Error("two commands were in flight at once, which desynchronizes the stream")
	}
	if len(f.calls) != 8 {
		t.Errorf("the server saw %d commands, want 8", len(f.calls))
	}
}
