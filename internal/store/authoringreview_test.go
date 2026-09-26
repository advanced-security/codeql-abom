package store

import (
	"errors"
	"testing"
)

func aReview(request int64, verdict string) AuthoringReview {
	return AuthoringReview{RequestID: request, QueryID: "acme/pin", QueryVersion: "v1", TestDigest: "d1",
		Actual: "| checkout.yml:9:7 | actions/checkout is pinned to v4 |\n", Verdict: verdict,
		ReviewedBy: "operator"}
}

// A review is what somebody decided about a report, so it keeps the report they were shown.
func TestAReviewKeepsWhatWasShownAndWhoDecided(t *testing.T) {
	s := newTestStore(t)
	request, _ := aSessionOn(t, s)
	rejected := aReview(request, ReviewRejected)
	rejected.Reason = "the short SHA is reported, and a full one is required"
	if _, err := s.RecordAuthoringReview(rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAuthoringReview(aReview(request, ReviewApproved)); err != nil {
		t.Fatal(err)
	}

	got, err := s.AuthoringReviews(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Verdict != ReviewApproved || got[1].Verdict != ReviewRejected ||
		got[1].Reason != rejected.Reason || got[1].Actual != rejected.Actual ||
		got[0].ReviewedBy != "operator" || got[0].ReviewedAt == "" || got[0].QueryVersion != "v1" ||
		got[0].TestDigest != "d1" {
		t.Errorf("reviews %+v, want the approval then the rejection, each with what was shown", got)
	}
	if none, err := s.AuthoringReviews(request + 100); err != nil || none == nil || len(none) != 0 {
		t.Errorf("no reviews: %v %v, want an empty list", none, err)
	}
}

// The next session is handed a rejection's reason, so one that gives none hands it nothing.
func TestAReviewIsRefusedWithoutWhatMakesItReadable(t *testing.T) {
	s := newTestStore(t)
	request, _ := aSessionOn(t, s)
	with := func(change func(*AuthoringReview)) AuthoringReview {
		r := aReview(request, ReviewApproved)
		change(&r)
		return r
	}
	for name, r := range map[string]AuthoringReview{
		"a rejection with no reason": aReview(request, ReviewRejected),
		"no verdict":                 with(func(r *AuthoringReview) { r.Verdict = "maybe" }),
		"nobody reviewing":           with(func(r *AuthoringReview) { r.ReviewedBy = " " }),
		"no query":                   with(func(r *AuthoringReview) { r.QueryID = "" }),
		"no version":                 with(func(r *AuthoringReview) { r.QueryVersion = "" }),
	} {
		if _, err := s.RecordAuthoringReview(r); err == nil {
			t.Errorf("%s was recorded", name)
		}
	}
	if _, err := s.RecordAuthoringReview(aReview(request+100, ReviewApproved)); !errors.Is(err, ErrNoQueryRequest) {
		t.Errorf("a review of no request: %v, want ErrNoQueryRequest", err)
	}
	if got, err := s.AuthoringReviews(request); err != nil || len(got) != 0 {
		t.Errorf("refused reviews were kept: %+v %v", got, err)
	}
}
