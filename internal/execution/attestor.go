package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// Attestor holds what a worker must not hand to anything it runs: the identity its pool signs as, its
// attempt's own key, the object store and the decision bundle. A worker reads its grant and what the
// grant binds, and writes what its attempt leaves, only through one; in a pod, across a socket from
// another container that reads nothing the work wrote.
type Attestor interface {
	// Grant is the grant the attempt runs under, verified, and its digest.
	Grant(ctx context.Context) (Grant, string, error)
	// Input reads one thing the grant binds, as exactly the bytes its digest names.
	Input(ctx context.Context, in Input) ([]byte, error)
	// Output writes one output of the attempt, by its name, kind and query, and lists it in the result.
	Output(ctx context.Context, o Output, body []byte) error
	// Report signs body as the attempt's next report and writes it, answering its place in the chain
	// and its digest once it is written.
	Report(ctx context.Context, body []byte) (int64, string, error)
	// Attest signs r, listing what was written, as the attempt's result and writes it last. Nothing is
	// written after.
	Attest(ctx context.Context, r Result) error
}

// Input is one thing a grant binds, by what it is and its digest.
type Input struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// What a grant binds a worker to read.
const (
	InputPack     = "pack"
	InputSnapshot = "snapshot"
	InputDatabase = "database"
)

// Secrets are what an attestor holds: the bundle it verifies the grant with, the identity it signs as
// and how to read it again, the attempt's key, and the object store.
type Secrets struct {
	Bundle     authz.Bundle
	Identity   *Identity
	Renew      func() (*Identity, error)
	AttemptKey *AttemptKey
	Objects    ObjectStore
	Now        func() time.Time
}

// Hold is the attestor holding s for the grant in env, which it verifies before anything is read or
// written.
func Hold(env authz.Envelope, s Secrets) Attestor { return &held{env: env, s: s} }

type held struct {
	env authz.Envelope
	s   Secrets

	mu       sync.Mutex
	opened   bool
	g        Grant
	digest   string
	openErr  error
	outputs  []Output
	seq      int64
	head     string
	attested bool
}

func (h *held) Grant(context.Context) (Grant, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.grant()
}

// grant is the verified grant, opened once; called holding mu.
func (h *held) grant() (Grant, string, error) {
	if !h.opened {
		h.g, h.digest, h.openErr = h.open()
		h.opened = true
	}
	return h.g, h.digest, h.openErr
}

// open verifies the grant as this attempt's, unexpired, for the identity and key held, answering it
// with its digest.
func (h *held) open() (Grant, string, error) {
	now := time.Now()
	if h.s.Now != nil {
		now = h.s.Now()
	}
	st, err := h.s.Bundle.Open(h.env, now)
	if err != nil {
		return Grant{}, "", fmt.Errorf("worker: the grant does not verify: %w", err)
	}
	if st.PredicateType != authz.PredicateTaskGrant {
		return Grant{}, "", fmt.Errorf("worker: %s is not a task grant", st.PredicateType)
	}
	var g Grant
	if err := json.Unmarshal(st.Predicate, &g); err != nil {
		return Grant{}, "", fmt.Errorf("worker: reading the grant: %w", err)
	}
	if h.s.Identity == nil || g.Identity != h.s.Identity.ID {
		return Grant{}, "", fmt.Errorf("worker: the grant is for %s, and this worker is not", g.Identity)
	}
	if h.s.AttemptKey == nil || g.AttemptKey != h.s.AttemptKey.PublicPEM() {
		return Grant{}, "", errors.New("worker: the grant names another key than the one this attempt was lent")
	}
	if until, err := time.Parse(time.RFC3339Nano, g.NotAfter); err != nil || !now.Before(until) {
		return Grant{}, "", errors.New("worker: the grant has expired")
	}
	grantDigest, err := h.env.Digest()
	return g, grantDigest, err
}

func (h *held) Input(ctx context.Context, in Input) ([]byte, error) {
	h.mu.Lock()
	g, _, err := h.grant()
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	key, limit, ok := bound(g, in)
	if !ok {
		return nil, fmt.Errorf("the grant binds no %s %s", in.Kind, in.SHA256)
	}
	body, err := h.s.Objects.Get(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	if digest(body) != in.SHA256 {
		return nil, fmt.Errorf("the %s read is not the %s granted", in.Kind, in.Kind)
	}
	return body, nil
}

// bound is where the input in is read from and how much of it, if g binds it. Only a digest the grant
// names is ever part of a key, and what is read there is handed over only as that digest's bytes.
func bound(g Grant, in Input) (string, int64, bool) {
	switch in.Kind {
	case InputPack:
		packs := []string{g.PackSHA256}
		if g.Test != nil {
			packs = append(packs, g.Test.PackSHA256)
		}
		if g.Session != nil {
			packs = append(packs, g.Session.PackSHA256)
		}
		return packKey(g.Prefix, in.SHA256), maxObject, slices.Contains(packs, in.SHA256)
	case InputSnapshot:
		return snapshotKey(g.Prefix, g.Attempt), maxObject, g.Session != nil && g.Session.SnapshotSHA256 == in.SHA256
	case InputDatabase:
		var sums []string
		if g.Session != nil {
			for _, sum := range g.Session.Databases {
				sums = append(sums, sum)
			}
		}
		return databaseKey(g.Prefix, g.Attempt, in.SHA256), databaseLimit, slices.Contains(sums, in.SHA256)
	}
	return "", 0, false
}

func (h *held) Output(ctx context.Context, o Output, body []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, _, err := h.grant()
	switch {
	case err != nil:
		return err
	case h.attested:
		return errors.New("the attempt has attested, and writes nothing after")
	case !plain(o.Name):
		return fmt.Errorf("%q is not a name under the attempt", o.Name)
	case len(h.outputs) >= maxOutputs:
		return fmt.Errorf("the attempt has written %d outputs, as many as a result may list", len(h.outputs))
	}
	if err := h.s.Objects.Put(ctx, outputKey(g.Prefix, g.Attempt, o.Name), body); err != nil {
		return err
	}
	h.outputs = append(h.outputs, Output{Name: o.Name, Kind: o.Kind, QueryID: o.QueryID, SHA256: digest(body),
		Size: int64(len(body))})
	return nil
}

func (h *held) Report(ctx context.Context, body []byte) (int64, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, grantDigest, err := h.grant()
	switch {
	case err != nil:
		return 0, "", err
	case h.attested:
		return 0, "", errors.New("the attempt has attested, and reports nothing after")
	}
	id := h.identity()
	rep := SessionReport{Attempt: g.Attempt, Grant: grantDigest, Identity: id.ID, Chain: id.ChainPEM(),
		Seq: h.seq + 1, Prev: h.head, Body: body}
	predicate, err := json.Marshal(rep)
	if err != nil {
		return 0, "", err
	}
	// Written even as the session stops, which is when its last report is made.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	env, err := h.seal(ctx, id, authz.Statement{PredicateType: authz.PredicateSessionReport, Predicate: predicate,
		Subject: []authz.Subject{{Name: g.Attempt, Digest: map[string]string{"sha256": grantDigest}},
			{Name: fmt.Sprintf("report/%d", rep.Seq), Digest: map[string]string{"sha256": digest(body)}}}})
	if err != nil {
		return 0, "", err
	}
	signed, err := json.Marshal(env)
	if err != nil {
		return 0, "", err
	}
	if err := h.s.Objects.Put(ctx, reportKey(g.Prefix, g.Attempt, rep.Seq), signed); err != nil {
		return 0, "", err
	}
	h.seq, h.head = rep.Seq, digest(signed)
	return h.seq, h.head, nil
}

func (h *held) Attest(ctx context.Context, r Result) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, grantDigest, err := h.grant()
	switch {
	case err != nil:
		return err
	case h.attested:
		return errors.New("the attempt has attested once")
	}
	r.Attempt, r.Grant, r.Outputs = g.Attempt, grantDigest, slices.Clone(h.outputs)
	if r.Outputs == nil {
		r.Outputs = []Output{}
	}
	if err := h.attest(ctx, g, r); err != nil {
		return err
	}
	h.attested = true
	return nil
}

// attest signs r as the identity held now and the attempt's key, and writes it last: its presence is
// what says the outputs are complete.
func (h *held) attest(ctx context.Context, g Grant, r Result) error {
	id := h.identity()
	r.Identity, r.Chain = id.ID, id.ChainPEM()
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	subjects := []authz.Subject{{Name: g.Attempt, Digest: map[string]string{"sha256": r.Grant}}}
	for _, o := range r.Outputs {
		subjects = append(subjects, authz.Subject{Name: o.Name, Digest: map[string]string{"sha256": o.SHA256}})
	}
	env, err := h.seal(ctx, id, authz.Statement{Subject: subjects,
		PredicateType: authz.PredicateResultAttestation, Predicate: body})
	if err != nil {
		return err
	}
	signed, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return h.s.Objects.Put(ctx, attestationKey(g.Prefix, g.Attempt), signed)
}

// seal signs st as id and with the attempt's key.
func (h *held) seal(ctx context.Context, id *Identity, st authz.Statement) (authz.Envelope, error) {
	signers := []authz.Signer{id}
	if h.s.AttemptKey != nil {
		signers = append(signers, h.s.AttemptKey)
	}
	return authz.SealBy(ctx, st, signers...)
}

// identity is who the attestor signs as now: renewed where it can be, and never as anyone else.
func (h *held) identity() *Identity {
	if h.s.Renew != nil {
		if id, err := h.s.Renew(); err == nil && id.ID == h.s.Identity.ID {
			return id
		}
	}
	return h.s.Identity
}

// Bounds on what an attestor reads of a request, which a worker that is taken over writes.
const (
	maxInputRequest = 4 << 10
)

// ServeAttestor answers a's worker on ln until ctx is done.
func ServeAttestor(ctx context.Context, ln net.Listener, a Attestor) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /grant", func(w http.ResponseWriter, r *http.Request) {
		g, grantDigest, err := a.Grant(r.Context())
		if err != nil {
			refuse(w, err)
			return
		}
		writeAttested(w, struct {
			Grant  Grant  `json:"grant"`
			Digest string `json:"digest"`
		}{g, grantDigest})
	})
	mux.HandleFunc("POST /input", func(w http.ResponseWriter, r *http.Request) {
		var in Input
		body, err := requestBody(w, r, maxInputRequest)
		if err == nil {
			err = json.Unmarshal(body, &in)
		}
		if err == nil {
			body, err = a.Input(r.Context(), in)
		}
		if err != nil {
			refuse(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("POST /output", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o := Output{Name: q.Get("name"), Kind: q.Get("kind"), QueryID: q.Get("query")}
		limit := int64(maxObject)
		if o.Kind == KindTestDatabase {
			limit = databaseLimit
		}
		body, err := requestBody(w, r, limit)
		if err == nil {
			err = a.Output(r.Context(), o, body)
		}
		if err != nil {
			refuse(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /report", func(w http.ResponseWriter, r *http.Request) {
		body, err := requestBody(w, r, maxReport)
		var seq int64
		var head string
		if err == nil {
			seq, head, err = a.Report(r.Context(), body)
		}
		if err != nil {
			refuse(w, err)
			return
		}
		writeAttested(w, struct {
			Seq  int64  `json:"seq"`
			Head string `json:"head"`
		}{seq, head})
	})
	mux.HandleFunc("POST /attest", func(w http.ResponseWriter, r *http.Request) {
		var result Result
		body, err := requestBody(w, r, maxSigned)
		if err == nil {
			err = json.Unmarshal(body, &result)
		}
		if err == nil {
			err = a.Attest(r.Context(), result)
		}
		if err != nil {
			refuse(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-stopped
		return nil
	}
	return err
}

// requestBody reads a request's body, refusing one longer than limit before reading any of it where
// its length is declared.
func requestBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	if r.ContentLength > limit {
		return nil, fmt.Errorf("%w: %d bytes, past %d", ErrTooLarge, r.ContentLength, limit)
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, fmt.Errorf("%w: past %d bytes", ErrTooLarge, limit)
	}
	return body, err
}

func writeAttested(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		refuse(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// refuse answers err with the status the worker reads it back as.
func refuse(w http.ResponseWriter, err error) {
	status := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	http.Error(w, err.Error(), status)
}

// DialAttestor is the worker's side of the attestor serving on socket, which it waits for: the
// attestor starts beside the worker, not before it.
func DialAttestor(socket string) Attestor {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline := time.Now().Add(attestorWait)
		for {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socket)
			if err == nil || ctx.Err() != nil || time.Now().After(deadline) {
				return conn, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return &remote{client: &http.Client{Transport: &http.Transport{DialContext: dial, DisableCompression: true}}}
}

// attestorWait is how long a worker waits for its attestor to listen.
const attestorWait = time.Minute

type remote struct{ client *http.Client }

func (c *remote) Grant(ctx context.Context) (Grant, string, error) {
	var got struct {
		Grant  Grant  `json:"grant"`
		Digest string `json:"digest"`
	}
	body, err := c.call(ctx, http.MethodGet, "/grant", nil)
	if err == nil {
		err = json.Unmarshal(body, &got)
	}
	return got.Grant, got.Digest, err
}

func (c *remote) Input(ctx context.Context, in Input) ([]byte, error) {
	req, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, http.MethodPost, "/input", req)
}

func (c *remote) Output(ctx context.Context, o Output, body []byte) error {
	q := url.Values{"name": {o.Name}, "kind": {o.Kind}}
	if o.QueryID != "" {
		q.Set("query", o.QueryID)
	}
	_, err := c.call(ctx, http.MethodPost, "/output?"+q.Encode(), body)
	return err
}

func (c *remote) Report(ctx context.Context, body []byte) (int64, string, error) {
	// Made even as the session stops, which is when its last report is made.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var got struct {
		Seq  int64  `json:"seq"`
		Head string `json:"head"`
	}
	answer, err := c.call(ctx, http.MethodPost, "/report", body)
	if err == nil {
		err = json.Unmarshal(answer, &got)
	}
	return got.Seq, got.Head, err
}

func (c *remote) Attest(ctx context.Context, r Result) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, http.MethodPost, "/attest", body)
	return err
}

// call makes one request of the attestor, answering what it answered or what it refused.
func (c *remote) call(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://attestor"+path, reader)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching the attestor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, databaseLimit+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 == 2 {
		return answer, nil
	}
	refusal := &refusedByAttestor{msg: strings.TrimSpace(string(answer))}
	switch resp.StatusCode {
	case http.StatusNotFound:
		refusal.is = ErrNotFound
	case http.StatusRequestEntityTooLarge:
		refusal.is = ErrTooLarge
	}
	return nil, refusal
}

// refusedByAttestor is what an attestor refused, as it said it, and as the refusal it was.
type refusedByAttestor struct {
	msg string
	is  error
}

func (r *refusedByAttestor) Error() string { return r.msg }
func (r *refusedByAttestor) Unwrap() error { return r.is }
