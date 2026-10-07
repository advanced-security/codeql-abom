package ghapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Relay hands each delivery on a smee.io channel, unchanged, to ABOM's receiver; what it misses is lost.
type Relay struct {
	// Source is not logged: whoever knows it can read every delivery.
	Source string
	// Target is ABOM's receiver, POST /api/events/github.
	Target string
	// HTTP must not time out a stream meant to stay open.
	HTTP *http.Client
	// Deliver posts to the receiver; nil uses a client with a timeout.
	Deliver *http.Client
	Log     io.Writer
	Retry   time.Duration
}

const (
	defaultRetry = 5 * time.Second
	// deliverAttempts covers a receiver still starting up; an answer, even a refusal, is never retried.
	deliverAttempts = 5
	// maxRelayEvent is GitHub's 25 MB payload cap with room for the headers around it.
	maxRelayEvent = 32 << 20
)

// perHop headers described the request to the relay, not the one made to the receiver.
var perHop = map[string]bool{"host": true, "content-length": true, "connection": true,
	"transfer-encoding": true, "keep-alive": true, "te": true, "trailer": true, "upgrade": true,
	"accept-encoding": true, "proxy-connection": true}

// channelFields sit beside the headers in a smee.io event and are not headers.
var channelFields = map[string]bool{"body": true, "query": true, "timestamp": true}

var forwardHTTP = &http.Client{Timeout: 30 * time.Second}

// Run relays until ctx ends, reconnecting after the stream drops.
func (r *Relay) Run(ctx context.Context) error {
	if r.Source == "" || r.Target == "" {
		return errors.New("a relay needs a channel to read and a receiver to deliver to")
	}
	retry := r.retry()
	for {
		err := r.stream(ctx)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		r.logf("relay: the channel on %s dropped (%v); deliveries sent while disconnected are lost, "+
			"and the recheck sweep is what notices them", hostOf(r.Source), err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(retry):
		}
	}
}

func (r *Relay) retry() time.Duration {
	if r.Retry <= 0 {
		return defaultRetry
	}
	return r.Retry
}

func (r *Relay) stream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Source, nil)
	if err != nil {
		return withoutURL(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	client := r.HTTP
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the channel answered %d", resp.StatusCode)
	}
	r.logf("relay: reading the channel on %s, delivering to %s", hostOf(r.Source), r.Target)
	return readEvents(resp.Body, func(event string, data []byte) {
		if event == "" || event == "message" {
			r.forward(ctx, data)
		}
	})
}

// readEvents parses a server-sent event stream, calling deliver once per complete event.
func readEvents(body io.Reader, deliver func(event string, data []byte)) error {
	reader := bufio.NewReaderSize(body, 64<<10)
	var event string
	var data []byte
	hasData := false
	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case err != nil:
		case len(line) == 0:
			if hasData && len(data) <= maxRelayEvent {
				deliver(event, data)
			}
			event, data, hasData = "", nil, false
		case line[0] == ':':
		default:
			field, value, _ := bytes.Cut(line, []byte(":"))
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(field) {
			case "event":
				event = string(value)
			case "data":
				if hasData {
					data = append(data, '\n')
				}
				if len(data)+len(value) <= maxRelayEvent+1 {
					data = append(data, value...)
				}
				hasData = true
			}
		}
		if errors.Is(err, io.EOF) {
			return errors.New("the channel closed the stream")
		}
		if err != nil {
			return err
		}
	}
}

func (r *Relay) forward(ctx context.Context, data []byte) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		r.logf("relay: skipped an event that is not a delivery: %v", err)
		return
	}
	body, ok := fields["body"]
	if !ok {
		r.logf("relay: skipped an event that carries no body")
		return
	}
	header := http.Header{}
	for name, raw := range fields {
		if channelFields[name] || perHop[strings.ToLower(name)] {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			header.Set(name, value)
		}
	}
	event, id := header.Get("X-GitHub-Event"), header.Get("X-GitHub-Delivery")
	resp, err := r.deliver(ctx, header, body)
	if err != nil {
		r.logf("relay: %s %s not delivered: %v", event, id, withoutURL(err))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	note := ""
	if resp.StatusCode == http.StatusUnauthorized {
		note = ": its signature was refused"
	}
	r.logf("relay: %s %s answered %d%s", event, id, resp.StatusCode, note)
}

// deliver posts one delivery, trying again only when the receiver could not be reached at all.
func (r *Relay) deliver(ctx context.Context, header http.Header, body []byte) (*http.Response, error) {
	client := r.Deliver
	if client == nil {
		client = forwardHTTP
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header = header.Clone()
		resp, err := client.Do(req)
		if err == nil || attempt == deliverAttempts {
			return resp, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.retry()):
		}
	}
}

func (r *Relay) logf(format string, args ...any) {
	if r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "the relay"
}
