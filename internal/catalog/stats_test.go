package catalog

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// Unchanged answers must be counted, so cache use is visible.
func TestUnchangedAnswersAreCountedRatherThanAbsorbed(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, listBody)
	})
	c.Cache = newFakeCache()

	if s := c.Stats(); s.Requests != 0 || s.NotModified != 0 {
		t.Fatalf("a client that has done nothing has spent nothing; got %+v", s)
	}

	for i := 0; i < 3; i++ {
		if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
			t.Fatal(err)
		}
	}

	got := c.Stats()
	if got.Requests != 3 {
		t.Errorf("three reads were made; got %d", got.Requests)
	}
	if got.NotModified != 2 {
		t.Errorf("two of three answers were unchanged; got %d", got.NotModified)
	}
}

// A read that GitHub answered in full is not an unchanged one, and counting it as such would
// report a saving nobody made.
func TestAFullAnswerIsNotCountedAsUnchanged(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, listBody)
	})
	c.Cache = newFakeCache()

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats(); got.Requests != 1 || got.NotModified != 0 {
		t.Errorf("one full read and no unchanged answer; got %+v", got)
	}
}
