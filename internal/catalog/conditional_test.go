package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeCache is a ResponseCache that records what was asked of it.
type fakeCache struct {
	mu      sync.Mutex
	etags   map[string]string
	bodies  map[string][]byte
	saves   int
	lookups int
}

func newFakeCache() *fakeCache {
	return &fakeCache{etags: map[string]string{}, bodies: map[string][]byte{}}
}

func (f *fakeCache) Lookup(url string) (string, []byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	etag, ok := f.etags[url]
	if !ok {
		return "", nil, false
	}
	return etag, f.bodies[url], true
}

func (f *fakeCache) Save(url, etag string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.etags[url] = etag
	f.bodies[url] = body
}

// The point of a conditional request: GitHub answers 304, the answer is the same, and the primary
// rate limit is not charged for it.
func TestAnUnchangedAnswerIsRecognizedWithoutAskingForItAgain(t *testing.T) {
	var conditional, unconditional int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		unconditional++
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, listBody)
	})
	c.Cache = newFakeCache()

	first, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("an unchanged answer must not be an error: %v", err)
	}

	if unconditional != 1 || conditional != 1 {
		t.Errorf("want one full read then one conditional; got %d full, %d conditional",
			unconditional, conditional)
	}
	if len(first) != len(second) {
		t.Fatalf("a 304 must answer with what was already read; got %d then %d",
			len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("entry %d differs across a 304: %+v then %+v", i, first[i], second[i])
		}
	}
}

func TestTheFirstRequestCarriesNoValidator(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("If-None-Match"); v != "" {
			t.Errorf("nothing was cached, so the request must carry no validator; got %q", v)
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, listBody)
	})
	cache := newFakeCache()
	c.Cache = cache

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if cache.saves != 1 {
		t.Errorf("an answer carrying an ETag is worth remembering; saved %d times", cache.saves)
	}
}

// A database is a zip of arbitrary size streamed to disk. Buffering it to remember it would trade
// the bounded memory this client is careful about for a saving it cannot make.
func TestADatabaseDownloadIsNeverRemembered(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("If-None-Match"); v != "" {
			t.Errorf("a download must not be conditional; got %q", v)
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, "not really a zip")
	})
	cache := newFakeCache()
	c.Cache = cache

	dest := t.TempDir() + "/db.zip"
	if err := c.DownloadDatabase(context.Background(), "octo-org/app", "actions", dest); err != nil {
		t.Fatal(err)
	}
	if cache.saves != 0 {
		t.Errorf("a database download must not enter the cache; saved %d times", cache.saves)
	}
}

// The failure mode worth guarding: a 304 nothing can answer must be an error, never an empty
// result. An organization that reads as having no repositories is a coverage lie, not a cheap read.
func TestA304WithNothingToReplayIsAnErrorRatherThanAnEmptyAnswer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	// A cache that claims a validator and has forgotten the body it belonged to.
	cache := newFakeCache()
	cache.etags["x"] = `"v1"`
	c.Cache = forgetfulCache{cache}

	_, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err == nil {
		t.Fatal("a 304 with nothing to replay must not read as an empty answer")
	}
	if !strings.Contains(err.Error(), "304") && !strings.Contains(err.Error(), "not modified") {
		t.Errorf("the error has to say what happened; got %v", err)
	}
}

// forgetfulCache answers with a validator and no body, which is the state an eviction between the
// lookup and the response would leave behind.
type forgetfulCache struct{ inner *fakeCache }

func (f forgetfulCache) Lookup(string) (string, []byte, bool) { return `"v1"`, nil, true }
func (f forgetfulCache) Save(url, etag string, body []byte)   { f.inner.Save(url, etag, body) }
