package serve

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

var webhookSecret = []byte("It's a Secret to Everybody")

func signedWith(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const pushBody = `{"ref":"refs/heads/main","before":"a000000000000000000000000000000000000001",` +
	`"after":"b000000000000000000000000000000000000002","created":false,"deleted":false,` +
	`"repository":{"full_name":"acme/service","default_branch":"main"},"sender":{"login":"octocat"}}`

func deliver(t *testing.T, srv *Server, body []byte, signature, id, event, contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := localRequest(http.MethodPost, "/api/events/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	if id != "" {
		req.Header.Set("X-GitHub-Delivery", id)
	}
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func receiving(t *testing.T) (*Server, *store.Store, *int) {
	t.Helper()
	srv, s := newServer(t)
	arrived := 0
	srv.Events = &EventReceiver{Secret: webhookSecret, Arrived: func() { arrived++ }}
	return srv, s, &arrived
}

func received(t *testing.T, s *store.Store) int {
	t.Helper()
	log, err := s.EventLog(10)
	if err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	return log.Received
}

// Refused deliveries never reach the log the processor parses; signed ones are kept byte for byte.
func TestADeliveryNotSignedUnderTheSecretIsRefusedBeforeAnythingParsesIt(t *testing.T) {
	srv, s, arrived := receiving(t)
	body := []byte(pushBody)

	for name, signature := range map[string]string{
		"no signature":   "",
		"another secret": signedWith([]byte("guess"), body),
		"another body":   signedWith(webhookSecret, []byte(`{"ref":"refs/heads/evil"}`)),
		"not a digest":   "sha256=not-hex",
	} {
		rec := deliver(t, srv, body, signature, "d-"+name, "push", "application/json")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401: %s", name, rec.Code, rec.Body.String())
		}
	}
	if n := received(t, s); n != 0 {
		t.Errorf("%d refused deliveries reached the log", n)
	}
	if *arrived != 0 {
		t.Errorf("the processor was woken %d times for refused deliveries", *arrived)
	}

	malformed := []byte(`{"ref": "refs/heads/main",`)
	rec := deliver(t, srv, malformed, signedWith(webhookSecret, malformed), "d-ok", "push",
		"application/json")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a signed delivery was refused with %d: %s", rec.Code, rec.Body.String())
	}
	d, found, err := s.Delivery("d-ok")
	if err != nil || !found || !bytes.Equal(d.Payload, malformed) {
		t.Errorf("Delivery = %q (found %v, %v), want the body exactly as signed", d.Payload, found, err)
	}
}

// A receiver with no secret would accept an HMAC anyone can compute, so it accepts nothing.
func TestWithoutASecretEveryDeliveryIsRefused(t *testing.T) {
	for name, receiver := range map[string]*EventReceiver{
		"no receiver": nil,
		"no secret":   {Secret: nil},
		"empty":       {Secret: []byte{}},
	} {
		t.Run(name, func(t *testing.T) {
			srv, s := newServer(t)
			srv.Events = receiver
			body := []byte(pushBody)
			rec := deliver(t, srv, body, signedWith(nil, body), "d-1", "push", "application/json")
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status %d, want 503: %s", rec.Code, rec.Body.String())
			}
			if n := received(t, s); n != 0 {
				t.Errorf("%d deliveries recorded with no secret configured", n)
			}
		})
	}
}

// A 2xx means on disk; a store that cannot commit answers an error GitHub can redeliver after.
func TestADeliveryIsAcknowledgedOnlyOnceItIsCommitted(t *testing.T) {
	srv, s, arrived := receiving(t)
	body := []byte(pushBody)

	rec := deliver(t, srv, body, signedWith(webhookSecret, body), "d-1", "push", "application/json")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	d, found, err := s.Delivery("d-1")
	if err != nil || !found {
		t.Fatalf("Delivery = found %v, %v", found, err)
	}
	if d.Event != "push" || d.Status != store.EventPending || d.ReceivedAt == "" ||
		!bytes.Equal(d.Payload, body) {
		t.Errorf("recorded %+v (payload %q), want the push as received, pending", d, d.Payload)
	}
	if *arrived != 1 {
		t.Errorf("processor woken %d times, want 1", *arrived)
	}

	_ = s.Close()
	other := []byte(strings.Replace(pushBody, "main", "release", 1))
	rec = deliver(t, srv, other, signedWith(webhookSecret, other), "d-2", "push", "application/json")
	if rec.Code < 500 {
		t.Errorf("a delivery the store could not commit was answered %d", rec.Code)
	}
}

// A redelivery is answered, so GitHub stops sending it, and recorded once, so it is acted on once.
func TestARedeliveryIsAcknowledgedAndRecordedOnce(t *testing.T) {
	srv, s, arrived := receiving(t)
	body := []byte(pushBody)

	first := deliver(t, srv, body, signedWith(webhookSecret, body), "d-1", "push", "application/json")
	again := deliver(t, srv, body, signedWith(webhookSecret, body), "d-1", "push", "application/json")
	if first.Code != http.StatusAccepted || again.Code != http.StatusOK {
		t.Errorf("statuses %d then %d, want 202 then 200", first.Code, again.Code)
	}
	if n := received(t, s); n != 1 {
		t.Errorf("received = %d, want 1", n)
	}
	if *arrived != 1 {
		t.Errorf("processor woken %d times, want once for one new delivery", *arrived)
	}
}

// What the receiver cannot act on is refused after the signature, not recorded as if it could be.
func TestASignedDeliveryMissingWhatIdentifiesItIsRefused(t *testing.T) {
	srv, s, _ := receiving(t)
	body := []byte(pushBody)
	signature := signedWith(webhookSecret, body)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no delivery id": deliver(t, srv, body, signature, "", "push", "application/json"),
		"no event":       deliver(t, srv, body, signature, "d-1", "", "application/json"),
	} {
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	form := []byte("payload=" + pushBody)
	rec := deliver(t, srv, form, signedWith(webhookSecret, form), "d-2", "push",
		"application/x-www-form-urlencoded")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form-encoded: status %d, want 415", rec.Code)
	}
	if n := received(t, s); n != 0 {
		t.Errorf("%d deliveries recorded", n)
	}
}

// A zero would read as a measurement of quiet repositories, so nothing received says so instead.
func TestAConfiguredReceiverThatHasReceivedNothingSaysSo(t *testing.T) {
	srv, s := newServer(t)

	off := decode[EventReceiverStatus](t, get(t, srv, "/api/events/github"))
	if off.Configured || off.State != EventsNotConfigured {
		t.Errorf("unconfigured status = %+v", off)
	}

	srv.Events = &EventReceiver{Secret: webhookSecret}
	rec := get(t, srv, "/api/events/github")
	quiet := decode[EventReceiverStatus](t, rec)
	if !quiet.Configured || quiet.State != EventsNothingReceived || quiet.Received != nil {
		t.Errorf("configured, nothing received = %+v", quiet)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if string(raw["received"]) != "null" {
		t.Errorf(`"received" = %s, want null rather than a count`, raw["received"])
	}

	body := []byte(pushBody)
	deliver(t, srv, body, signedWith(webhookSecret, body), "d-1", "push", "application/json")
	if _, err := s.EventLog(10); err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	busy := decode[EventReceiverStatus](t, get(t, srv, "/api/events/github"))
	if busy.State != EventsReceiving || busy.Received == nil || *busy.Received != 1 {
		t.Errorf("after one delivery = %+v", busy)
	}
	if busy.Last == nil || busy.Last.DeliveryID != "d-1" || busy.Last.Event != "push" {
		t.Errorf("last = %+v, want d-1", busy.Last)
	}
	if busy.Statuses[store.EventPending] != 1 {
		t.Errorf("statuses = %v, want one pending", busy.Statuses)
	}
	if strings.Contains(get(t, srv, "/api/events/github").Body.String(), string(webhookSecret)) {
		t.Error("the status carries the secret")
	}
}

// Retention keeps 30 days of deliveries, so a count without the moment it starts from reads as all-time.
func TestTheReceiverSaysSinceWhenItsCountsRun(t *testing.T) {
	srv, s, _ := receiving(t)
	status := func() map[string]json.RawMessage {
		t.Helper()
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(get(t, srv, "/api/events/github").Body.Bytes(), &raw); err != nil {
			t.Fatalf("decoding the status: %v", err)
		}
		return raw
	}

	if raw, present := status()["since"]; !present || string(raw) != "null" {
		t.Errorf(`with nothing kept, "since" is %s (present: %v), want present and null rather than a time`,
			raw, present)
	}

	body := []byte(pushBody)
	deliver(t, srv, body, signedWith(webhookSecret, body), "d-1", "push", "application/json")
	deliver(t, srv, body, signedWith(webhookSecret, body), "d-2", "push", "application/json")
	first, _, err := s.Delivery("d-1")
	if err != nil {
		t.Fatal(err)
	}
	want, err := time.Parse(time.RFC3339Nano, first.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}

	busy := status()
	var since time.Time
	if err := json.Unmarshal(busy["since"], &since); err != nil || !since.Equal(want) {
		t.Errorf(`"since" = %s (%v), want %v, when the oldest kept delivery arrived`, busy["since"], err, want)
	}
	if string(busy["received"]) != "2" {
		t.Errorf(`"received" = %s, want 2 since then`, busy["received"])
	}
}
