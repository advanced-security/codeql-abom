package serve

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type replicaBackend struct {
	store.Backend
	state store.ReplicaState
	nodes int
}

func (b *replicaBackend) ReplicationState() (store.ReplicaState, error) { return b.state, nil }
func (b *replicaBackend) Summary() (store.Summary, error) {
	return store.Summary{Nodes: b.nodes}, nil
}

func TestReplicaRoutingReportsLagAndPreservesReadYourWrites(t *testing.T) {
	now := time.Now()
	replayed := now.Add(-2 * time.Second)
	primary := &replicaBackend{state: store.ReplicaState{Identity: "same-store", Position: "0/20", ObservedAt: now}, nodes: 20}
	replica := &replicaBackend{state: store.ReplicaState{
		Identity: "same-store", Recovery: true, Position: "0/10", ReplayAt: &replayed, ObservedAt: now,
	}, nodes: 10}
	s := &Server{Store: primary, Replica: &Server{Store: replica}, MaxReplicaLag: 3 * time.Second}
	handler := s.Handler()
	read := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		request := localRequest(http.MethodGet, "/api/summary", nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := read(nil)
	if first.Header().Get("X-Abom-Read-Source") != "replica" || first.Header().Get("X-Abom-Replica-Lag-Ms") != "2000" {
		t.Fatalf("replica lag was not reported: %v", first.Header())
	}
	consistent := read(&http.Cookie{Name: primaryReadCookie, Value: "1"})
	if consistent.Header().Get("X-Abom-Read-Source") != "primary" {
		t.Fatal("a client that wrote was sent to a replica missing its write")
	}
	replica.state.Position = primary.state.Position
	caughtUp := read(&http.Cookie{Name: primaryReadCookie, Value: "1"})
	if caughtUp.Header().Get("X-Abom-Read-Source") != "replica" {
		t.Fatal("a caught-up replica was not eligible for consistent reads")
	}
	if len(caughtUp.Result().Cookies()) != 1 || caughtUp.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("the read-consistency cookie was not cleared after catch-up")
	}
	replica.state.Identity = "different-store"
	if wrong := read(nil); wrong.Header().Get("X-Abom-Read-Source") != "primary" {
		t.Fatal("an unrelated PostgreSQL cluster was treated as this store's replica")
	}
	replica.state.Identity = primary.state.Identity
	replica.state.Position, replica.state.ReplayAt = "0/10", &now
	s.MaxReplicaLag = 0
	if strict := read(nil); strict.Header().Get("X-Abom-Read-Source") != "primary" {
		t.Fatal("zero lag tolerance accepted a replica whose WAL position was behind")
	}
	replica.state.Position, replica.state.ReplayAt = "0/10", nil
	unknown := read(nil)
	if unknown.Header().Get("X-Abom-Read-Source") != "primary" ||
		unknown.Header().Get("X-Abom-Replica-Lag-Ms") != "unknown" {
		t.Fatal("an unmeasured replica lag was reported as zero or served as current")
	}
}

func TestReplicaModeMarksWritesForPrimaryReadback(t *testing.T) {
	s := &Server{Replica: &Server{}}
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	replica := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("a write reached the replica") })
	response := httptest.NewRecorder()
	s.routeReplica(primary, replica).ServeHTTP(response, localRequest(http.MethodPost, "/api/control", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != primaryReadCookie || cookies[0].Value != "1" {
		t.Fatal("a write did not establish read-your-writes routing")
	}
}

func TestReaderInstancesCannotStartMutatingHandlers(t *testing.T) {
	s := &Server{ReadOnly: true, AllowRemoteControl: true}
	response := httptest.NewRecorder()
	request := localRequest(http.MethodPost, "/api/discovery/start", nil)
	request.Header.Set("X-Abom-Control", "1")
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader instance entered a control handler: %d", response.Code)
	}
}

// A paused replay, its lag and a refused write are what the stubbed replica above cannot show.
func TestPhysicalReplicaReportsLagAndKeepsAControlsOwnWriteVisible(t *testing.T) {
	primaryTarget, standbyTarget, onStandby := physicalStandby(t)
	primary, err := store.OpenBackend(primaryTarget, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primary.Close() })
	if err := primary.Migrate(); err != nil {
		t.Fatal(err)
	}
	var replica store.Backend
	waitFor(t, func() bool {
		replica, err = store.OpenBackend(standbyTarget, store.Options{ReadOnly: true})
		return err == nil
	})
	t.Cleanup(func() { _ = replica.Close() })
	waitFor(t, func() bool {
		got, err := replica.OwnedScope()
		if err != nil || len(got) != 0 {
			return false
		}
		state, err := replica.ReplicationState()
		return err == nil && state.Identity != ""
	})
	if err := onStandby("SELECT pg_wal_replay_pause();"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = onStandby("SELECT pg_wal_replay_resume();") })
	s := &Server{Store: primary, AllowRemoteControl: true}
	s.SetReplica(replica, time.Hour)
	handler := s.Handler()
	write := localRequest(http.MethodPut, "/api/views/replica-readback",
		strings.NewReader(`{"predicate":"scope=owned"}`))

	write.Header.Set("X-Abom-Control", "1")
	write.Header.Set("Content-Type", "application/json")
	written := httptest.NewRecorder()
	handler.ServeHTTP(written, write)
	if written.Code >= 400 {
		t.Fatalf("control write: %d %s", written.Code, written.Body.String())
	}
	cookies := written.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("the control response did not establish read consistency")
	}
	read := func(cookie bool) *httptest.ResponseRecorder {
		request := localRequest(http.MethodGet, "/api/views", nil)
		if cookie {
			request.AddCookie(cookies[0])
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	current := read(true)
	if current.Header().Get("X-Abom-Read-Source") != "primary" ||
		!strings.Contains(current.Body.String(), "replica-readback") {
		t.Fatalf("own write vanished behind a paused replica: %v %s", current.Header(), current.Body.String())
	}
	stale := read(false)
	if stale.Header().Get("X-Abom-Read-Source") != "replica" ||
		stale.Header().Get("X-Abom-Replica-Lag-Ms") == "unknown" ||
		strings.Contains(stale.Body.String(), "replica-readback") {
		t.Fatalf("bounded replica read was mislabeled: %v %s", stale.Header(), stale.Body.String())
	}
	if err := replica.SetDiscoveryIntent(true); err == nil {
		t.Fatal("a reader backend wrote through the physical standby")
	}
	if err := onStandby("SELECT pg_wal_replay_resume();"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		response := read(true)
		return response.Header().Get("X-Abom-Read-Source") == "replica" &&
			strings.Contains(response.Body.String(), "replica-readback")
	})
}

// physicalStandby names one fresh database on the Docker harness's primary and on its standby, and
// runs statements on the standby. It skips outside the harness, as the CodeQL integration tests do;
// a run that selected a store backend is the harness, and a missing standby there is a failure.
func physicalStandby(t *testing.T) (primary, standby string, onStandby func(string) error) {
	t.Helper()
	if os.Getenv("ABOM_TEST_STORE_BACKEND") == "" {
		t.Skip("needs the Docker PostgreSQL primary and its physical standby: run make backend-test")
	}
	primaryURL, err := url.Parse(os.Getenv("ABOM_TEST_POSTGRES_DSN"))
	if err != nil || primaryURL.User == nil {
		t.Fatal("the PostgreSQL harness supplied no primary; run make backend-test")
	}
	standbyURL, err := url.Parse(os.Getenv("ABOM_TEST_POSTGRES_REPLICA_DSN"))
	container := os.Getenv("ABOM_TEST_POSTGRES_CONTAINER")
	standbyContainer := os.Getenv("ABOM_TEST_POSTGRES_REPLICA_CONTAINER")
	if err != nil || standbyURL.Host == "" || container == "" || standbyContainer == "" {
		t.Fatal("the PostgreSQL harness supplied no physical standby; run make backend-test")
	}
	user := primaryURL.User.Username()
	name := "abom_" + strings.ToLower(rand.Text())
	if err := psql(container, user, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := psql(container, user, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	primaryURL.Path, standbyURL.Path = "/"+name, "/"+name
	return primaryURL.String(), standbyURL.String(), func(statement string) error {
		return psql(standbyContainer, user, statement)
	}
}

// psql runs one statement inside a harness container, where the server and its superuser are local.
func psql(container, user, statement string) error {
	output, err := exec.Command("docker", "exec", container, "psql", "--username", user, "--dbname", "postgres",
		"--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--command", statement).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", statement, err, output)
	}
	return nil
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-timeout.C:
			t.Fatal("PostgreSQL replication did not reach the required state")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-tick.C:
		}
	}
}
