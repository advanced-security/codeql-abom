package store

import (
	"bytes"
	"testing"
)

// A validator is only worth holding if the answer it belongs to comes back byte for byte. A body
// that round-trips as anything else would be replayed to a decoder as though GitHub had sent it.
func TestAHeldAnswerComesBackUnchanged(t *testing.T) {
	s := newTestStore(t)
	c := s.ResponseCache()
	body := []byte(`[{"name":"app","archived":false}]`)

	c.Save("https://api.github.com/orgs/acme/repos?page=1", `W/"abc"`, body)

	etag, got, ok := c.Lookup("https://api.github.com/orgs/acme/repos?page=1")
	if !ok {
		t.Fatal("what was saved has to be findable")
	}
	if etag != `W/"abc"` {
		t.Errorf("the validator must survive verbatim, weak marker included; got %q", etag)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("the body must round-trip byte for byte; got %q", got)
	}
}

func TestAURLNothingWasSavedForIsAMiss(t *testing.T) {
	s := newTestStore(t)
	if _, _, ok := s.ResponseCache().Lookup("https://api.github.com/orgs/acme/repos"); ok {
		t.Error("nothing was saved, so there is nothing to replay")
	}
}

// One entry per URL. Letting a URL accumulate validators would mean guessing which of them GitHub
// meant, and the newest answer is the only one it will ever confirm.
func TestSavingAgainReplacesWhatTheURLHeld(t *testing.T) {
	s := newTestStore(t)
	c := s.ResponseCache()
	url := "https://api.github.com/orgs/acme/repos"

	c.Save(url, `"v1"`, []byte("first"))
	c.Save(url, `"v2"`, []byte("second"))

	etag, body, ok := c.Lookup(url)
	if !ok {
		t.Fatal("the URL still holds an answer")
	}
	if etag != `"v2"` || string(body) != "second" {
		t.Errorf("the later answer replaces the earlier one; got %q / %q", etag, body)
	}

	var rows int
	if err := s.DB().QueryRow(`SELECT count(*) FROM abom_http_cache WHERE url = ?`, url).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("a URL holds one entry, not a history; got %d rows", rows)
	}
}
