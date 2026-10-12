package demo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
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

// Egress is a forward proxy that opens TLS tunnels to allowed hosts and nothing else. It never
// terminates TLS, so it decides by host alone, and it answers only loopback clients.
type Egress struct {
	// Allow names hosts exactly, or a suffix when it starts with a dot.
	Allow []string
	// Ports are the ports a tunnel may reach; empty means 443 alone.
	Ports []string
	Log   io.Writer

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
