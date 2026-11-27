package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// attested is an attestor holding f's secrets for a grant signed for g, served on a socket, and the
// worker's side of that socket.
func attested(t *testing.T, f *fixture, g Grant, key *AttemptKey) Attestor {
	t.Helper()
	env, _, err := f.runner.grant(context.Background(), g, "a test")
	if err != nil {
		t.Fatal(err)
	}
	trust, err := authz.ReadBundle(f.keys)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.ca.Issue(f.identity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return served(t, Hold(env, Secrets{Bundle: trust, Identity: id, AttemptKey: key, Objects: f.objects}))
}

// served is a, across a socket.
func served(t *testing.T, a Attestor) Attestor {
	t.Helper()
	dir, err := os.MkdirTemp("", "abom-attest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeAttestor(ctx, ln, a) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return DialAttestor(socket)
}

// grantFor is a grant of f's pool for a new attempt, and the key it names.
func grantFor(t *testing.T, f *fixture) (Grant, *AttemptKey) {
	t.Helper()
	attempt, err := newAttempt()
	if err != nil {
		t.Fatal(err)
	}
	g, private, err := f.runner.newGrant(attempt)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParseAttemptKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return g, key
}

// A worker asks its attestor for what its grant binds and nothing else: the attestor reads only the
// pack, snapshot and databases the grant names by digest, and hands them over only as the bytes that
// digest names.
func TestAnAttestorReadsOnlyWhatItsGrantBinds(t *testing.T) {
	f := newFixture(t, analysis)
	ctx := context.Background()
	pack, snapshot, database := []byte("a pack"), []byte(`{"session":7}`), []byte("a database")
	g, key := grantFor(t, f)
	g.Session = &SessionTask{Session: 7, Workspace: "/w", PackSHA256: digest(pack), SnapshotSHA256: digest(snapshot),
		Databases: map[string]string{"octo/app": digest(database)}}
	for k, body := range map[string][]byte{packKey(g.Prefix, digest(pack)): pack,
		snapshotKey(g.Prefix, g.Attempt): snapshot, databaseKey(g.Prefix, g.Attempt, digest(database)): database,
		packKey(g.Prefix, digest([]byte("another pack"))): []byte("another pack")} {
		if err := f.objects.Put(ctx, k, body); err != nil {
			t.Fatal(err)
		}
	}
	a := attested(t, f, g, key)
	if _, _, err := a.Grant(ctx); err != nil {
		t.Fatal(err)
	}
	for _, in := range []Input{{InputPack, digest(pack)}, {InputSnapshot, digest(snapshot)}, {InputDatabase, digest(database)}} {
		if _, err := a.Input(ctx, in); err != nil {
			t.Errorf("the attestor refused %+v, which the grant binds: %v", in, err)
		}
	}
	for _, in := range []Input{{InputPack, digest([]byte("another pack"))}, {InputDatabase, digest(pack)},
		{InputSnapshot, digest(pack)}, {"grant", digest(pack)}, {InputDatabase, "../../" + digest(database)}} {
		if body, err := a.Input(ctx, in); err == nil {
			t.Errorf("the attestor handed over %q for %+v, which the grant does not bind", body, in)
		}
	}
	// What another attempt put where this one reads is not handed over, by its own digest or the one
	// granted.
	swapped, other := []byte(`{"session":8}`), []byte("another database")
	for k, body := range map[string][]byte{snapshotKey(g.Prefix, g.Attempt): swapped,
		databaseKey(g.Prefix, g.Attempt, digest(other)): other} {
		if err := f.objects.Put(ctx, k, body); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []Input{{InputSnapshot, digest(snapshot)}, {InputSnapshot, digest(swapped)}, {InputDatabase, digest(other)}} {
		if body, err := a.Input(ctx, in); err == nil {
			t.Errorf("the attestor handed over %q for %+v", body, in)
		}
	}
}

// keeping is an object store that keeps whatever it is handed under whatever name, as one that does
// not read a name as a path would.
type keeping struct {
	mu   sync.Mutex
	kept map[string][]byte
}

func (k *keeping) Put(_ context.Context, key string, body []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.kept == nil {
		k.kept = map[string][]byte{}
	}
	k.kept[key] = body
	return nil
}

func (k *keeping) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if body, ok := k.kept[key]; ok {
		return body, nil
	}
	return nil, ErrNotFound
}

// What a worker says it wrote is not what the result lists: the attestor lists what was written
// through it, each by the digest of what it wrote, under the attempt, and nothing once it has attested.
func TestAnAttestorListsWhatWasWrittenThroughIt(t *testing.T) {
	f := newFixture(t, analysis)
	ctx := context.Background()
	g, key := grantFor(t, f)
	store := &keeping{}
	f.runner.Objects = store
	env, _, err := f.runner.grant(ctx, g, "a test")
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := authz.ReadBundle(f.keys)
	id, _ := f.ca.Issue(f.identity, time.Hour)
	a := served(t, Hold(env, Secrets{Bundle: trust, Identity: id, AttemptKey: key, Objects: store}))
	_, grantDigest, err := a.Grant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Output(ctx, Output{Name: "results/abom_edges.json", Kind: KindEdges}, []byte(`{"edges":[]}`)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape.json", "/abs.json", "results/../../x", ""} {
		if err := a.Output(ctx, Output{Name: name, Kind: KindEdges}, []byte(`{}`)); err == nil {
			t.Errorf("the attestor wrote %q", name)
		}
	}
	var written []string
	for k := range store.kept {
		if k != grantKey(g.Prefix, g.Attempt) {
			written = append(written, k)
		}
	}
	if len(written) != 1 || written[0] != outputKey(g.Prefix, g.Attempt, "results/abom_edges.json") {
		t.Errorf("the attestor wrote %v; want the one output named under the attempt", written)
	}
	claimed := Result{Commit: "abc123", Outputs: []Output{{Name: "results/forged.json", Kind: KindEdges, SHA256: digest([]byte("x")), Size: 1}}}
	if err := a.Attest(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	result, err := f.runner.verify(ctx, g.Attempt, grantDigest, mustGet(t, store, attestationKey(g.Prefix, g.Attempt)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].Name != "results/abom_edges.json" ||
		result.Outputs[0].SHA256 != digest([]byte(`{"edges":[]}`)) || result.Commit != "abc123" {
		t.Errorf("the attestation lists %+v; want what was written alone", result)
	}
	if err := a.Output(ctx, Output{Name: "results/late.json", Kind: KindEdges}, []byte(`{}`)); err == nil {
		t.Error("the attestor wrote an output once it had attested")
	}
	if _, _, err := a.Report(ctx, []byte(`{}`)); err == nil {
		t.Error("the attestor made a report once it had attested")
	}
	if err := a.Attest(ctx, Result{}); err == nil {
		t.Error("the attestor attested twice")
	}
}

// A grant the attestor cannot verify is refused before anything is read or written, and so is a grant
// naming another key than the one the attestor was lent.
func TestAnAttestorRefusesAGrantItCannotHoldTo(t *testing.T) {
	f := newFixture(t, analysis)
	ctx := context.Background()
	g, key := grantFor(t, f)
	other, _, err := NewAttemptKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]Attestor{
		"lent another key":     attested(t, f, g, other),
		"lent no key":          attested(t, f, g, nil),
		"for another identity": attested(t, f, func() Grant { h := g; h.Identity = "spiffe://abom.local/ns/abom/sa/x"; return h }(), key),
		"past its time": attested(t, f, func() Grant {
			h := g
			h.NotAfter = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			return h
		}(), key),
	} {
		if _, _, err := a.Grant(ctx); err == nil {
			t.Errorf("an attestor %s held to the grant", name)
		}
		if err := a.Output(ctx, Output{Name: "x", Kind: KindEdges}, []byte("x")); err == nil {
			t.Errorf("an attestor %s wrote an output", name)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(string(f.objects), filepath.FromSlash(outputKey(g.Prefix, g.Attempt, "")))); len(entries) != 0 {
		t.Errorf("refused attestors wrote %d outputs", len(entries))
	}
}

// The attestor starts beside the worker, not before it, so a worker waits for its socket to appear; and
// what the attestor refuses reaches the worker as the refusal it was.
func TestAWorkerWaitsForItsAttestorAndHearsItsRefusals(t *testing.T) {
	f := newFixture(t, analysis)
	ctx := context.Background()
	g, key := grantFor(t, f)
	g.PackSHA256 = digest([]byte("a pack"))
	env, _, err := f.runner.grant(ctx, g, "a test")
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := authz.ReadBundle(f.keys)
	id, _ := f.ca.Issue(f.identity, time.Hour)
	dir, err := os.MkdirTemp("", "abom-attest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "a.sock")
	a := DialAttestor(socket)
	serving, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		ln, err := net.Listen("unix", socket)
		if err != nil {
			t.Error(err)
			return
		}
		_ = ServeAttestor(serving, ln, Hold(env, Secrets{Bundle: trust, Identity: id, AttemptKey: key, Objects: f.objects}))
	}()
	if _, _, err := a.Grant(ctx); err != nil {
		t.Fatalf("a worker started before its attestor: %v", err)
	}
	if _, err := a.Input(ctx, Input{InputPack, digest([]byte("a pack"))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a pack that is not there reached the worker as %v", err)
	}
	if _, _, err := a.Report(ctx, make([]byte, maxReport+1)); !errors.Is(err, ErrTooLarge) ||
		strings.Contains(err.Error(), "connection") {
		t.Errorf("a report past what one may be reached the worker as %v", err)
	}
}

// A result lists no more outputs than a coordinator reads of one, so the attestor writes no more.
func TestAnAttestorWritesNoMoreOutputsThanAResultMayList(t *testing.T) {
	f := newFixture(t, analysis)
	ctx := context.Background()
	g, key := grantFor(t, f)
	env, _, err := f.runner.grant(ctx, g, "a test")
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := authz.ReadBundle(f.keys)
	id, _ := f.ca.Issue(f.identity, time.Hour)
	a := Hold(env, Secrets{Bundle: trust, Identity: id, AttemptKey: key, Objects: discard{}})
	for i := range maxOutputs {
		if err := a.Output(ctx, Output{Name: fmt.Sprintf("lock/%d", i), Kind: KindLock}, nil); err != nil {
			t.Fatalf("output %d: %v", i, err)
		}
	}
	if err := a.Output(ctx, Output{Name: "lock/one-more", Kind: KindLock}, nil); err == nil {
		t.Errorf("the attestor wrote output %d", maxOutputs+1)
	}
}

// discard is an object store that keeps nothing.
type discard struct{}

func (discard) Put(context.Context, string, []byte) error { return nil }
func (discard) Get(context.Context, string, int64) ([]byte, error) {
	return nil, ErrNotFound
}

// A worker that is taken over does not use DialAttestor: it writes the socket itself. A body declaring
// more than may be read is refused before any of it is read, and one declaring nothing is read no
// further than may be.
func TestAnAttestorReadsNoMoreOfARequestThanItMay(t *testing.T) {
	f := newFixture(t, analysis)
	g, key := grantFor(t, f)
	dir, err := os.MkdirTemp("", "abom-attest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := f.runner.grant(context.Background(), g, "a test")
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := authz.ReadBundle(f.keys)
	id, _ := f.ca.Issue(f.identity, time.Hour)
	var reported int
	a := counting{Attestor: Hold(env, Secrets{Bundle: trust, Identity: id, AttemptKey: key, Objects: f.objects}), reports: &reported}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeAttestor(ctx, ln, a) }()
	t.Cleanup(func() { cancel(); <-done })
	ask := func(request string) string {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		// The attestor answers and closes once it has read all it may, while the rest is still written.
		go func() { _, _ = conn.Write([]byte(request)) }()
		answer := make([]byte, 64)
		n, _ := io.ReadAtLeast(conn, answer, 12)
		return string(answer[:n])
	}
	// Declared past the bound, and no body follows: refused without waiting for one.
	if got := ask(fmt.Sprintf("POST /report HTTP/1.1\r\nHost: a\r\nContent-Length: %d\r\n\r\n", maxReport+1)); !strings.HasPrefix(got, "HTTP/1.1 413") {
		t.Errorf("a report declaring more than may be read was answered %q", got)
	}
	// Undeclared, and longer than the bound: read no further.
	chunk := strings.Repeat("x", 1<<20)
	var body strings.Builder
	for range maxReport/len(chunk) + 1 {
		fmt.Fprintf(&body, "%x\r\n%s\r\n", len(chunk), chunk)
	}
	body.WriteString("0\r\n\r\n")
	if got := ask("POST /report HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked\r\n\r\n" + body.String()); !strings.HasPrefix(got, "HTTP/1.1 413") {
		t.Errorf("a report longer than may be, declaring no length, was answered %q", got)
	}
	if reported != 0 {
		t.Errorf("the attestor made %d reports of requests past what it may read", reported)
	}
}

// counting is an attestor that counts the reports it is asked to make.
type counting struct {
	Attestor
	reports *int
}

func (c counting) Report(ctx context.Context, body []byte) (int64, string, error) {
	*c.reports++
	return c.Attestor.Report(ctx, body)
}
