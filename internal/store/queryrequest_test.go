package store

import (
	"strings"
	"testing"
)

func aRequest() QueryRequest {
	return QueryRequest{
		Description:  "a self-hosted runner reached from a pull_request trigger",
		Compliant:    "runs-on: ubuntu-latest",
		NonCompliant: "runs-on: self-hosted",
		RequestedBy:  "operator",
	}
}

// Without both examples a request has nothing to turn into a test.
func TestAQueryRequestNeedsADescriptionAndBothExamples(t *testing.T) {
	s := newTestStore(t)
	for name, blank := range map[string]func(*QueryRequest){
		"description":   func(r *QueryRequest) { r.Description = "  " },
		"compliant":     func(r *QueryRequest) { r.Compliant = "" },
		"non-compliant": func(r *QueryRequest) { r.NonCompliant = "\n" },
		"requester":     func(r *QueryRequest) { r.RequestedBy = "" },
	} {
		r := aRequest()
		blank(&r)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("a request with no %s was kept", name)
		}
	}
	if got, err := s.QueryRequests(); err != nil || len(got) != 0 {
		t.Errorf("refused requests left %d rows (err %v)", len(got), err)
	}
}

func TestAQueryRequestIsKeptOpenAndSaysWhoAsked(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	r := got[0]
	if r.RowID != id || r.Status != RequestOpen || r.RequestedBy != "operator" || r.CreatedAt == "" {
		t.Errorf("kept %+v", r)
	}
	if r.NonCompliant != "runs-on: self-hosted" {
		t.Errorf("the non-compliant example came back as %q", r.NonCompliant)
	}
}

// An edit changes what is asked for, never who asked or when: the request is theirs.
func TestEditingAQueryRequestKeepsWhoAskedAndWhen(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.QueryRequests()

	edit := aRequest()
	edit.RowID = id
	edit.RequestedBy = "somebody-else"
	edit.Status = RequestInProgress
	edit.Kind = RequestMissed
	edit.QueryID = "acme/self-hosted-runner"
	if _, err := s.SaveQueryRequest(edit); err != nil {
		t.Fatal(err)
	}

	after, _ := s.QueryRequests()
	if len(after) != 1 {
		t.Fatalf("an edit left %d requests", len(after))
	}
	r := after[0]
	if r.RequestedBy != "operator" || r.CreatedAt != before[0].CreatedAt {
		t.Errorf("an edit rewrote the requester or the date: %+v", r)
	}
	if r.Status != RequestInProgress || r.Kind != RequestMissed || r.QueryID != "acme/self-hosted-runner" {
		t.Errorf("the edit was not kept: %+v", r)
	}
}

func TestAQueryRequestRefusesAValueOutsideItsVocabulary(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.Status = "closed"
	if _, err := s.SaveQueryRequest(r); err == nil || !strings.Contains(err.Error(), RequestDeclined) {
		t.Errorf("an unknown status was not refused with the alternatives: %v", err)
	}
	r = aRequest()
	r.Kind = "bug"
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("an unknown kind was kept")
	}
}

func TestEditingARequestThatDoesNotExistIsRefused(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.RowID = 41
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("an edit of a request nobody made was accepted")
	}
}

func TestNoQueryRequestsIsAnEmptyListNotNull(t *testing.T) {
	s := newTestStore(t)
	got, err := s.QueryRequests()
	if err != nil || got == nil {
		t.Errorf("got %v, %v; want an empty list", got, err)
	}
}
