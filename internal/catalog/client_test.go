package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New(srv.URL, "test-token", srv.Client())
	// Keep retry backoff out of test runtime.
	c.sleep = func(time.Duration) {}
	return c
}

const listBody = `[
  {
    "id": 1,
    "name": "actions-database.zip",
    "language": "actions",
    "content_type": "application/zip",
    "size": 4096,
    "created_at": "2026-08-01T10:00:00Z",
    "updated_at": "2026-08-02T11:00:00Z",
    "url": "https://api.github.com/repos/octo-org/app/code-scanning/codeql/databases/actions",
    "commit_oid": "11bd71901bbe5b1630ceea73d27597364c9af683"
  },
  {
    "id": 2,
    "language": "javascript",
    "size": 8192,
    "commit_oid": "aaaa71901bbe5b1630ceea73d27597364c9af683",
    "url": "https://api.github.com/repos/octo-org/app/code-scanning/codeql/databases/javascript"
  }
]`

func TestListDatabasesParsesMetadata(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/code-scanning/codeql/databases"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		fmt.Fprint(w, listBody)
	})

	dbs, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) != 2 {
		t.Fatalf("got %d databases, want 2", len(dbs))
	}

	got := dbs[0]
	if got.Language != "actions" {
		t.Errorf("Language = %q", got.Language)
	}
	if got.CommitOID != "11bd71901bbe5b1630ceea73d27597364c9af683" {
		t.Errorf("CommitOID = %q", got.CommitOID)
	}
	if got.Size != 4096 {
		t.Errorf("Size = %d, want 4096", got.Size)
	}
	if got.Repository != "octo-org/app" {
		t.Errorf("Repository = %q, want octo-org/app", got.Repository)
	}
}

// The commit is what lets the ledger skip unchanged repositories before paying to download a
// database, which is the difference between 20,000 downloads and a handful.
func TestFindDatabaseReturnsCommitWithoutDownloading(t *testing.T) {
	var downloads int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/databases") {
			fmt.Fprint(w, listBody)
			return
		}
		atomic.AddInt32(&downloads, 1)
		w.Write([]byte("zip"))
	})

	db, err := c.FindDatabase(context.Background(), "octo-org/app", "actions")
	if err != nil {
		t.Fatalf("FindDatabase: %v", err)
	}
	if db.CommitOID == "" {
		t.Error("CommitOID is empty")
	}
	if n := atomic.LoadInt32(&downloads); n != 0 {
		t.Errorf("FindDatabase performed %d downloads, want 0", n)
	}
}

func TestFindDatabaseMissingLanguage(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, listBody)
	})

	_, err := c.FindDatabase(context.Background(), "octo-org/app", "ruby")
	if !errors.Is(err, ErrNoDatabase) {
		t.Errorf("got %v, want ErrNoDatabase", err)
	}
}

// Most repositories in a large organisation have no actions database; that must be an
// expected, countable outcome rather than a hard failure.
func TestListDatabasesNotFoundIsSentinel(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := c.ListDatabases(context.Background(), "octo-org/app")
	if !errors.Is(err, ErrNoDatabase) {
		t.Errorf("got %v, want ErrNoDatabase", err)
	}
}

func TestAuthorizationHeaderIsSent(t *testing.T) {
	var auth string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, listBody)
	})

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if auth != "Bearer test-token" {
		t.Errorf("Authorization = %q", auth)
	}
}

// Errors must never carry the token, since they are logged.
func TestErrorsDoNotLeakToken(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})

	_, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Errorf("error leaked the token: %v", err)
	}
}

func TestDownloadDatabaseWritesFile(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/databases") {
			fmt.Fprint(w, listBody)
			return
		}
		if got := r.Header.Get("Accept"); got != "application/zip" {
			t.Errorf("Accept = %q, want application/zip", got)
		}
		fmt.Fprint(w, "PK\x03\x04database-bytes")
	})

	dest := filepath.Join(t.TempDir(), "actions.zip")
	if err := c.DownloadDatabase(context.Background(), "octo-org/app", "actions", dest); err != nil {
		t.Fatalf("DownloadDatabase: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading download: %v", err)
	}
	if string(data) != "PK\x03\x04database-bytes" {
		t.Errorf("downloaded %q", data)
	}
}

// A partial download must not be mistaken for a complete database on the next run.
func TestDownloadDatabaseLeavesNoFileOnFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/databases") {
			fmt.Fprint(w, listBody)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	dest := filepath.Join(t.TempDir(), "actions.zip")
	if err := c.DownloadDatabase(context.Background(), "octo-org/app", "actions", dest); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed download left a file behind")
	}
}

// 20,000 repositories means tens of thousands of API calls, so rate limiting is a normal
// operating condition rather than an exceptional one.
func TestRetriesOnSecondaryRateLimit(t *testing.T) {
	var calls int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, listBody)
	})

	dbs, err := c.ListDatabases(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) != 2 {
		t.Errorf("got %d databases, want 2", len(dbs))
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("made %d calls, want 2", n)
	}
}

func TestRetriesOnPrimaryRateLimit(t *testing.T) {
	var calls int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("x-ratelimit-remaining", "0")
			w.Header().Set("x-ratelimit-reset", fmt.Sprint(time.Now().Add(time.Second).Unix()))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, listBody)
	})

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("made %d calls, want 2", n)
	}
}

// A 403 that is not a rate limit is a permission problem and must fail immediately.
func TestForbiddenWithoutRateLimitDoesNotRetry(t *testing.T) {
	var calls int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("x-ratelimit-remaining", "4999")
		w.WriteHeader(http.StatusForbidden)
	})

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err == nil {
		t.Fatal("expected an error")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("made %d calls, want 1", n)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	var calls int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.MaxRetries = 2

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("made %d calls, want 3 (initial + 2 retries)", n)
	}
}

func TestContextCancellationStopsRetrying(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.ListDatabases(ctx, "octo-org/app"); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestInvalidRepositoryName(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := c.ListDatabases(context.Background(), "no-slash"); err == nil {
		t.Error("expected an error for a malformed repository name")
	}
}
