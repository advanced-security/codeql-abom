package store

import (
	"strings"
	"testing"
	"time"
)

// A session is kept by the digest of its cookie, so a restart finds it and a copy of the database
// signs nobody in; it is gone once signed out, and an expired one is never answered.
func TestASessionIsKeptByItsDigestUntilItEndsOrExpires(t *testing.T) {
	var s Backend = newTestStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	if _, ok, err := s.Session("d1", now); ok || err != nil {
		t.Fatalf("an unknown session answered %v, %v", ok, err)
	}
	if err := s.PutSession("d1", []byte(`{"user":"ada"}`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Session("d1", now); !ok || err != nil || string(got) != `{"user":"ada"}` {
		t.Fatalf("Session = %q, %v, %v", got, ok, err)
	}
	if err := s.PutSession("d1", []byte(`{"user":"ada","role":"admin"}`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Session("d1", now); !strings.Contains(string(got), "admin") {
		t.Errorf("a session written again reads %q; want the newer record", got)
	}
	if _, ok, _ := s.Session("d1", now.Add(time.Hour+time.Second)); ok {
		t.Error("an expired session was answered")
	}
	if err := s.DeleteSession("d1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Session("d1", now); ok {
		t.Error("a session outlived being deleted")
	}
}

// Expired sessions are removed as new ones arrive, so the table holds the live ones and no more.
func TestExpiredSessionsAreRemovedAsOthersArrive(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if err := s.PutSession("old", []byte(`{}`), now.Add(-time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession("new", []byte(`{}`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_session`).Scan(&n); err != nil || n != 1 {
		t.Errorf("sessions held = %d, %v; want only the live one", n, err)
	}
}
