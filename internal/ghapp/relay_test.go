package ghapp_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// syncBuffer is a log both the relay and the test touch.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// smeeEvent is one delivery as smee.io streams it: lowercased headers beside body and query on one data line.
func smeeEvent(id, event, signature string, body []byte) string {
	return fmt.Sprintf(`data: {"host":"smee.io","x-github-event":%q,"x-github-delivery":%q,`+
		`"x-hub-signature-256":%q,"content-type":"application/json","user-agent":"GitHub-Hookshot/0a1b2c3",`+
		`"x-github-hook-id":"123456","content-length":"9999","connection":"close","body":%s,`+
		`"query":{},"timestamp":1759600000000}`+"\n\n", event, id, signature, body)
}

// channel streams the given events after smee's ready event, then holds the stream open.
func channel(t *testing.T, events ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			http.Error(w, "the channel page", http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 0\nevent: ready\ndata: {}\n\n")
		_, _ = io.WriteString(w, "id: 1\nevent: ping\ndata: {}\n\n")
		for _, e := range events {
			_, _ = io.WriteString(w, e)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A body whose bytes any JSON re-encoding would change: escaped slash, escaped non-ASCII, spacing.
var awkwardBody = []byte(`{"ref":"refs/heads/main", "after":"0123456789abcdef0123456789abcdef01234567",` +
	`"repository":{"full_name":"has-ghas/widgets"},"head_commit":{"message":"a\/b caf\u00e9 <ok> \ud83d\ude80"}}`)

// The relay forwards headers and the body's exact bytes; re-encoding the body would break every signature.
func TestTheRelayForwardsHeadersAndRawBodyUnchanged(t *testing.T) {
	type received struct {
		header http.Header
		body   []byte
	}
	got := make(chan received, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Clone(), body}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(target.Close)
	signature := sign(webhookSecret, awkwardBody)
	source := channel(t, smeeEvent("d-1", "push", signature, awkwardBody))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = (&ghapp.Relay{Source: source.URL, Target: target.URL + "/api/events/github",
			Log: io.Discard, Retry: time.Millisecond}).Run(ctx)
	}()

	select {
	case r := <-got:
		if !bytes.Equal(r.body, awkwardBody) {
			t.Errorf("body changed in the relay:\n got %s\nwant %s", r.body, awkwardBody)
		}
		for name, want := range map[string]string{
			"X-GitHub-Event": "push", "X-GitHub-Delivery": "d-1", "X-Hub-Signature-256": signature,
			"Content-Type": "application/json", "User-Agent": "GitHub-Hookshot/0a1b2c3", "X-GitHub-Hook-ID": "123456",
		} {
			if r.header.Get(name) != want {
				t.Errorf("%s = %q, want %q unchanged", name, r.header.Get(name), want)
			}
		}
		if r.header.Get("Content-Length") == "9999" || r.header.Get("Connection") == "close" {
			t.Errorf("a per-hop header was forwarded as if it described this request: %v", r.header)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was relayed")
	}
}

// A body altered in the relay fails the receiver's signature check and is never recorded: the relay is trusted with nothing.
func TestATamperedRelayedBodyFailsTheReceiversSignatureCheck(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer((&serve.Server{Store: s,
		Events: &serve.EventReceiver{Secret: []byte(webhookSecret)}}).Handler())
	t.Cleanup(receiver.Close)

	signature := sign(webhookSecret, awkwardBody)
	tampered := bytes.Replace(awkwardBody, []byte("refs/heads/main"), []byte("refs/heads/evil"), 1)
	source := channel(t,
		smeeEvent("d-good", "push", signature, awkwardBody),
		smeeEvent("d-bad", "push", signature, tampered))

	log := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = (&ghapp.Relay{Source: source.URL, Target: receiver.URL + "/api/events/github",
			Log: log, Retry: time.Millisecond}).Run(ctx)
	}()
	eventually(t, "both deliveries to be relayed", func() bool {
		return strings.Contains(log.String(), "d-good") && strings.Contains(log.String(), "d-bad")
	})

	good, found, err := s.Delivery("d-good")
	if err != nil || !found || !bytes.Equal(good.Payload, awkwardBody) {
		t.Errorf("the intact delivery = %+v found=%v err=%v; want it recorded byte for byte", good, found, err)
	}
	if _, found, _ := s.Delivery("d-bad"); found {
		t.Error("the tampered delivery was recorded")
	}
	if !strings.Contains(log.String(), "d-bad") || !strings.Contains(log.String(), "401") {
		t.Errorf("the relay's log does not say the receiver refused the tampered delivery:\n%s", log.String())
	}
	if strings.Contains(log.String(), signature) || strings.Contains(log.String(), "refs/heads") {
		t.Errorf("the relay logged a signature or a body:\n%s", log.String())
	}
}

// flakyTransport refuses the first connections, as a receiver still starting up does.
type flakyTransport struct {
	mu      sync.Mutex
	refuse  int
	calls   int
	through http.RoundTripper
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	refuse := f.refuse > 0
	f.refuse--
	f.mu.Unlock()
	if refuse {
		return nil, fmt.Errorf("dial tcp: connection refused")
	}
	return f.through.RoundTrip(r)
}

// A receiver that is not listening yet, as when serve starts its relay, is retried rather than losing the delivery.
func TestTheRelayRetriesAReceiverThatIsNotListeningYet(t *testing.T) {
	got := make(chan []byte, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- body
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(target.Close)
	source := channel(t, smeeEvent("d-late", "push", sign(webhookSecret, awkwardBody), awkwardBody))
	log := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flaky := &flakyTransport{refuse: 2, through: http.DefaultTransport}
	go func() {
		_ = (&ghapp.Relay{Source: source.URL, Target: target.URL, Log: log, Retry: time.Millisecond,
			Deliver: &http.Client{Transport: flaky}}).Run(ctx)
	}()
	select {
	case body := <-got:
		flaky.mu.Lock()
		calls := flaky.calls
		flaky.mu.Unlock()
		if !bytes.Equal(body, awkwardBody) || calls != 3 {
			t.Errorf("relayed %s after %d attempts; want the body unchanged on the third", body, calls)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("a delivery was lost to a receiver that was briefly not listening:\n%s", log.String())
	}
}
