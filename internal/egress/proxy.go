// Package egress is a forward proxy that reaches named hosts and nothing else: the Demo's, on loopback,
// and the one an authoring pod's agent reaches the network through in a cluster.
package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	stdpath "path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CopilotHosts are the hosts a Copilot session is expected to need. Anything else it asks for is
// refused and recorded, which is how a session's real needs are qualified.
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

// Proxy opens TLS tunnels to allowed hosts and nothing else. It never terminates TLS but for the hosts
// it inspects, so it decides by host alone.
type Proxy struct {
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
	// Admit says which clients are answered, by their address; nil answers any, for a proxy whose
	// clients are bounded by the network around it.
	Admit func(remote string) bool
	// Dial reaches an allowed host; nil reaches it only at public addresses.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// MaxTunnels bounds the tunnels open at once; zero is 256.
	MaxTunnels int

	mu   sync.Mutex
	open int
}

func (e *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.Admit != nil && !e.Admit(r.RemoteAddr) {
		e.record("denied a client at %s", r.RemoteAddr)
		http.Error(w, "the egress proxy does not answer "+r.RemoteAddr, http.StatusForbidden)
		return
	}
	if r.Method != http.MethodConnect {
		e.record("denied %s %s: only tunnels to allowed hosts are offered", r.Method, r.URL.Redacted())
		http.Error(w, "the egress proxy offers only tunnels to allowed hosts", http.StatusForbidden)
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
		http.Error(w, "the egress proxy does not reach "+r.Host, http.StatusForbidden)
		return
	}
	if !e.take() {
		e.record("denied CONNECT %s: %d tunnels are open", r.Host, e.maxTunnels())
		http.Error(w, "the egress proxy has as many tunnels open as it holds", http.StatusServiceUnavailable)
		return
	}
	upstream, err := e.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		e.release()
		e.record("failed CONNECT %s: %v", r.Host, err)
		http.Error(w, "the egress proxy could not reach "+r.Host, http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		e.release()
		_ = upstream.Close()
		http.Error(w, "tunneling is not supported here", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		e.release()
		_ = upstream.Close()
		return
	}
	e.record("allowed CONNECT %s", r.Host)
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	var closing sync.WaitGroup
	closing.Add(2)
	go func() {
		defer closing.Done()
		defer func() { _ = upstream.Close() }()
		_, _ = io.Copy(upstream, buffered)
	}()
	go func() {
		defer closing.Done()
		defer func() { _ = client.Close() }()
		_, _ = io.Copy(client, upstream)
	}()
	go func() {
		closing.Wait()
		e.release()
	}()
}

func (e *Proxy) maxTunnels() int {
	if e.MaxTunnels > 0 {
		return e.MaxTunnels
	}
	return 256
}

// take counts a tunnel opening, if there is room for one.
func (e *Proxy) take() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.open >= e.maxTunnels() {
		return false
	}
	e.open++
	return true
}

func (e *Proxy) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.open--
}

func (e *Proxy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if e.Dial != nil {
		return e.Dial(ctx, network, addr)
	}
	return dialPublic(ctx, network, addr)
}

// notPublic are the ranges outside the private, loopback and link-local ones that still lead inside
// a network or translate to an address that does: shared address space, benchmarking, IETF protocol
// assignments, and the IPv6 prefixes that carry an IPv4 address.
var notPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

// public reports whether ip is an address on the public internet.
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	return !slices.ContainsFunc(notPublic, func(p netip.Prefix) bool { return p.Contains(ip) })
}

// dialPublic reaches addr only once every address its name resolves to is public, and dials the
// addresses it checked, so a name an allowed suffix covers cannot be pointed inside the network.
func dialPublic(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !public(ip) {
			return nil, fmt.Errorf("%s resolves to %s, which is not a public address", host, ip)
		}
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	var errs []error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// inspect terminates TLS for an inspected host and forwards each request whose path it admits.
func (e *Proxy) inspect(w http.ResponseWriter, host, target string) {
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
		upstream = &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second,
			DialContext: e.dial}
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

func (e *Proxy) forward(req *http.Request, host, target string, upstream http.RoundTripper) *http.Response {
	path := req.URL.Path
	admitted := (req.Method == http.MethodGet || req.Method == http.MethodHead) &&
		path == stdpath.Clean(path) && slices.ContainsFunc(e.Inspect[host], func(prefix string) bool {
		return strings.HasPrefix(path, prefix)
	})
	if !admitted {
		e.record("denied %s %s%s", req.Method, host, path)
		return refusal(req, "the egress proxy does not reach "+host+path)
	}
	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.URL.Scheme, out.URL.Host = "https", target
	out.Host = ""
	resp, err := upstream.RoundTrip(out)
	if err != nil {
		e.record("failed %s %s%s: %v", req.Method, host, path, err)
		return refusal(req, "the egress proxy could not reach "+host)
	}
	e.record("allowed %s %s%s: %d", req.Method, host, path, resp.StatusCode)
	// The client spoke HTTP/1.1 to this proxy, whatever upstream answered in.
	resp.Proto, resp.ProtoMajor, resp.ProtoMinor = "HTTP/1.1", 1, 1
	// Without a length, HTTP/1.1 ends a body at close, and this connection stays open.
	if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 {
		resp.TransferEncoding = []string{"chunked"}
	}
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

func (e *Proxy) ports() []string {
	if len(e.Ports) == 0 {
		return []string{"443"}
	}
	return e.Ports
}

func (e *Proxy) allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range e.Allow {
		a = strings.ToLower(a)
		if host == a || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a) && len(host) > len(a)) {
			return true
		}
	}
	return false
}

func (e *Proxy) record(format string, args ...any) {
	if e.Log == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(e.Log, "%s egress: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// Loopback admits only clients on this host's loopback addresses.
func Loopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Listen serves the proxy on addr until ctx ends.
func Listen(ctx context.Context, addr string, e *Proxy) error {
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
