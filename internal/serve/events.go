package serve

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/advanced-security/codeql-abom/internal/events"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// EventReceiver holds the secret deliveries are signed with and a way to wake the processor.
type EventReceiver struct {
	Secret []byte
	// Arrived is called once a new delivery has committed. Nil leaves the processor to its period.
	Arrived func()
}

// What a receiver can say about whether it is receiving.
const (
	EventsNotConfigured   = "not configured"
	EventsNothingReceived = "nothing received"
	EventsReceiving       = "receiving"
)

// EventReceiverStatus leaves Received, Since and Statuses null until something arrives: quiet is not zero.
type EventReceiverStatus struct {
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	Received   *int   `json:"received"`
	// Since is when the oldest kept delivery arrived; Received and every count cover only what is kept.
	Since    *time.Time            `json:"since"`
	Last     *store.EventDelivery  `json:"last"`
	Statuses map[string]int        `json:"statuses"`
	Outcomes []store.EventOutcome  `json:"outcomes"`
	Failures []store.EventDelivery `json:"failures"`
}

// maxDelivery is GitHub's cap on a webhook payload.
const maxDelivery = 25 << 20

// recentFailures bounds how many failed deliveries the status lists.
const recentFailures = 20

type deliveryReceipt struct {
	DeliveryID string `json:"deliveryId"`
	Recorded   string `json:"recorded"`
}

// receiveGitHubEvent records verified deliveries unparsed; the signature replaces the control guard.
func (s *Server) receiveGitHubEvent(w http.ResponseWriter, r *http.Request) {
	if s.Events == nil || len(s.Events.Secret) == 0 {
		writeError(w, http.StatusServiceUnavailable,
			"no webhook secret is configured, so every delivery is refused")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDelivery))
	if err != nil {
		status := http.StatusBadRequest
		if tooLarge := new(http.MaxBytesError); errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, "reading the delivery: "+err.Error())
		return
	}
	if err := events.Verify(s.Events.Secret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	id, event := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event")
	if id == "" || event == "" {
		writeError(w, http.StatusBadRequest, "a delivery needs X-GitHub-Delivery and X-GitHub-Event")
		return
	}
	if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType,
			"this receiver reads JSON deliveries; set the webhook's content type to application/json")
		return
	}
	fresh, err := s.Store.AppendDelivery(store.EventDelivery{DeliveryID: id, Event: event, Payload: body})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "recording the delivery: "+err.Error())
		return
	}
	if !fresh {
		writeJSON(w, deliveryReceipt{DeliveryID: id, Recorded: "already"}, nil)
		return
	}
	if s.Events.Arrived != nil {
		s.Events.Arrived()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	// The record has committed; a client that went away has nothing left to learn.
	_ = json.NewEncoder(w).Encode(deliveryReceipt{DeliveryID: id, Recorded: "now"})
}

// gitHubEventStatus reports configuration, arrivals and every outcome with its reason.
func (s *Server) gitHubEventStatus(w http.ResponseWriter, r *http.Request) {
	log, err := s.Store.EventLog(recentFailures)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	configured := s.Events != nil && len(s.Events.Secret) > 0
	out := EventReceiverStatus{
		Configured: configured, Since: log.Since, Last: log.Last, Outcomes: log.Outcomes, Failures: log.Failures,
	}
	switch {
	case !configured:
		out.State = EventsNotConfigured
	case log.Received == 0:
		out.State = EventsNothingReceived
	default:
		out.State = EventsReceiving
	}
	if log.Received > 0 {
		received := log.Received
		out.Received, out.Statuses = &received, log.Statuses
	}
	writeJSON(w, out, nil)
}
