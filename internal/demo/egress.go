package demo

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	stdpath "path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CopilotHosts are the hosts a Demo's Copilot sessions are expected to need. Anything else a child
// asks for is refused and recorded, which is how a session's real needs are qualified.
var CopilotHosts = []string{
	"api.githubcopilot.com",
	".githubcopilot.com",
	"copilot-proxy.githubusercontent.com",
}

// CopilotPaths are the Copilot APIs on hosts that also serve everything else: the CLI validates its
// token at api.github.com/copilot_internal/, beside every repository API, so that host is read
// request by request rather than tunneled.
var CopilotPaths = map[string][]string{
	"api.github.com": {"/copilot_internal/"},
}

// Egress is a forward proxy that opens TLS tunnels to allowed hosts and nothing else. It never
// terminates TLS, so it decides by host alone, and it answers only loopback clients.
type Egress struct {
	// Allow names hosts exactly, or a suffix when it starts with a dot.
	Allow []string
	// Ports are the ports a tunnel may reach; empty means 443 alone.
	Ports []string
	// Inspect names hosts whose requests are read, and the path prefixes forwarded for each; GET and
	// HEAD alone. Authority signs what the proxy presents for them, and children must trust it.
	Inspect   map[string][]string
	Authority *Authority
	// Upstream forwards what Inspect admits; nil verifies the real host as any client would.
	Upstream http.RoundTripper
	Log      io.Writer

	mu sync.Mutex
}

func (e *Egress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackPeer(r.RemoteAddr) {
		http.Error(w, "the Demo egress proxy answers only loopback clients", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodConnect {
		e.record("denied %s %s: only tunnels to allowed hosts are offered", r.Method, r.URL.Redacted())
		http.Error(w, "the Demo egress proxy offers only tunnels to allowed hosts", http.StatusForbidden)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	if _, inspect := e.Inspect[strings.ToLower(host)]; err == nil && inspect && e.Authority != nil &&
		slices.Contains(e.ports(), port) {
		e.inspect(w, strings.ToLower(host), r.Host)
		return
	}
	if err != nil || !e.allowed(host) || !slices.Contains(e.ports(), port) {
		e.record("denied CONNECT %s", r.Host)
		http.Error(w, "the Demo egress proxy does not reach "+r.Host, http.StatusForbidden)
		return
	}
	upstream, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		e.record("failed CONNECT %s: %v", r.Host, err)
		http.Error(w, "the Demo egress proxy could not reach "+r.Host, http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "tunneling is not supported here", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	e.record("allowed CONNECT %s", r.Host)
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go func() {
		defer func() { _ = upstream.Close() }()
		_, _ = io.Copy(upstream, buffered)
	}()
	go func() {
		defer func() { _ = client.Close() }()
		_, _ = io.Copy(client, upstream)
	}()
}

// inspect terminates TLS for an inspected host and forwards each request whose path it admits.
func (e *Egress) inspect(w http.ResponseWriter, host, target string) {
	leaf, err := e.Authority.leaf(host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunneling is not supported here", http.StatusInternalServerError)
		return
	}
	raw, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = raw.Close() }()
	_, _ = raw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	conn := tls.Server(raw, &tls.Config{
		Certificates: []tls.Certificate{*leaf}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12,
	})
	upstream := e.Upstream
	if upstream == nil {
		upstream = &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second}
	}
	reader := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		resp := e.forward(req, host, target, upstream)
		// A refused body is still on the connection; a large one ends it rather than being read.
		drained, _ := io.Copy(io.Discard, io.LimitReader(req.Body, 1<<20))
		closing := req.Close || resp.Close || drained == 1<<20
		err = resp.Write(conn)
		_ = resp.Body.Close()
		if err != nil || closing {
			return
		}
	}
}

func (e *Egress) forward(req *http.Request, host, target string, upstream http.RoundTripper) *http.Response {
	path := req.URL.Path
	admitted := (req.Method == http.MethodGet || req.Method == http.MethodHead) &&
		path == stdpath.Clean(path) && slices.ContainsFunc(e.Inspect[host], func(prefix string) bool {
		return strings.HasPrefix(path, prefix)
	})
	if !admitted {
		e.record("denied %s %s%s", req.Method, host, path)
		return refusal(req, "the Demo egress proxy does not reach "+host+path)
	}
	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.URL.Scheme, out.URL.Host = "https", target
	out.Host = ""
	resp, err := upstream.RoundTrip(out)
	if err != nil {
		e.record("failed %s %s%s: %v", req.Method, host, path, err)
		return refusal(req, "the Demo egress proxy could not reach "+host)
	}
	e.record("allowed %s %s%s: %d", req.Method, host, path, resp.StatusCode)
	// The client spoke HTTP/1.1 to this proxy, whatever upstream answered in.
	resp.Proto, resp.ProtoMajor, resp.ProtoMinor = "HTTP/1.1", 1, 1
	return resp
}

func refusal(req *http.Request, message string) *http.Response {
	body := `{"message":` + strconv.Quote(message) + "}"
	return &http.Response{
		StatusCode: http.StatusForbidden, ProtoMajor: 1, ProtoMinor: 1, Request: req,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func (e *Egress) ports() []string {
	if len(e.Ports) == 0 {
		return []string{"443"}
	}
	return e.Ports
}

func (e *Egress) allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range e.Allow {
		a = strings.ToLower(a)
		if host == a || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a) && len(host) > len(a)) {
			return true
		}
	}
	return false
}

func (e *Egress) record(format string, args ...any) {
	if e.Log == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(e.Log, "%s egress: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// ListenEgress serves the proxy on addr, which must be loopback, until ctx ends.
func ListenEgress(ctx context.Context, addr string, e *Egress) error {
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("the egress proxy listens on loopback only, not %q", addr)
	}
	srv := &http.Server{Addr: addr, Handler: e, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
