package demo

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func egressProxy(t *testing.T, e *Egress) *url.URL {
	t.Helper()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

// A tunnel to an allowed host is the one way out, and it carries the client's own TLS untouched.
func TestEgressTunnelsOnlyToAllowedHosts(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("copilot"))
	}))
	defer target.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "https://"))
	var log bytes.Buffer
	proxy := egressProxy(t, &Egress{Allow: []string{"localhost"}, Ports: []string{port}, Log: &log})
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the test server's own certificate
	}}
	resp, err := client.Get("https://localhost:" + port + "/")
	if err != nil {
		t.Fatalf("an allowed host: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an allowed host answered %d", resp.StatusCode)
	}
	if denied, err := client.Get("https://127.0.0.1:" + port + "/"); err == nil {
		denied.Body.Close()
		t.Fatal("a host outside the allowlist was tunneled to")
	}
	if !strings.Contains(log.String(), "denied CONNECT 127.0.0.1:"+port) {
		t.Fatalf("the denial was not recorded: %q", log.String())
	}
}

// Plain HTTP proxying would let a child reach any host in clear; only tunnels are offered.
func TestEgressRefusesEverythingButATunnel(t *testing.T) {
	proxy := egressProxy(t, &Egress{Allow: []string{"api.githubcopilot.com"}})
	conn, err := net.Dial("tcp", proxy.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET http://api.githubcopilot.com/ HTTP/1.1\r\nHost: api.githubcopilot.com\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a plain request through the proxy answered %d", resp.StatusCode)
	}
}

// The allowlist names hosts and suffixes exactly; a look-alike name does not match.
func TestEgressMatchesHostsExactly(t *testing.T) {
	e := &Egress{Allow: []string{"api.githubcopilot.com", ".individual.githubcopilot.com"}}
	for host, want := range map[string]bool{
		"api.githubcopilot.com":                  true,
		"API.githubcopilot.com":                  true,
		"proxy.individual.githubcopilot.com":     true,
		"individual.githubcopilot.com":           false,
		"api.githubcopilot.com.evil.example":     false,
		"evilapi.githubcopilot.com":              false,
		"api.github.com":                         false,
		"x.individual.githubcopilot.com.example": false,
	} {
		if got := e.allowed(host); got != want {
			t.Errorf("allowed(%s) = %v, want %v", host, got, want)
		}
	}
}

// api.github.com serves Copilot's token checks and every repository API on one host, so the proxy
// reads requests there and forwards only Copilot's paths; a tunnel would admit them all.
func TestEgressForwardsOnlyCopilotPathsOnAnInspectedHost(t *testing.T) {
	var hits []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"login":"demo"}`))
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "https://"))
	authority, err := NewAuthority([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	e := &Egress{
		Inspect: map[string][]string{"localhost": {"/copilot_internal/"}}, Authority: authority,
		Ports: []string{port}, Log: &log,
		Upstream: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: certPool(upstream.Certificate()), ServerName: "example.com"}},
	}
	proxy := egressProxy(t, e)
	trusted := x509.NewCertPool()
	trusted.AppendCertsFromPEM(authority.CertPEM())
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: trusted},
	}}

	resp, err := client.Get("https://localhost:" + port + "/copilot_internal/user")
	if err != nil {
		t.Fatalf("a Copilot path: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "demo") {
		t.Fatalf("a Copilot path answered %d %s", resp.StatusCode, body)
	}
	for _, req := range []struct{ method, path string }{
		{"GET", "/user"}, {"GET", "/repos/acme/app"}, {"POST", "/graphql"}, {"POST", "/copilot_internal/x"},
		{"GET", "/copilot_internal/../user"},
	} {
		r, _ := http.NewRequest(req.method, "https://localhost:"+port+req.path, strings.NewReader("{}"))
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", req.method, req.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s answered %d", req.method, req.path, resp.StatusCode)
		}
	}
	if len(hits) != 1 || hits[0] != "GET /copilot_internal/user" {
		t.Errorf("upstream received %v, want only the Copilot path", hits)
	}
	if !strings.Contains(log.String(), "denied GET localhost/user") {
		t.Errorf("the denial was not recorded: %q", log.String())
	}
}

// The authority that lets the proxy read api.github.com can sign nothing else, so a copy of it is
// no use against any other host.
func TestTheAuthorityIsConstrainedToTheInspectedHosts(t *testing.T) {
	a, err := NewAuthority([]string{"api.github.com"})
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(a.CertPEM())
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !ca.IsCA || !ca.PermittedDNSDomainsCritical ||
		len(ca.PermittedDNSDomains) != 1 || ca.PermittedDNSDomains[0] != "api.github.com" {
		t.Fatalf("CA constraints: IsCA=%v critical=%v domains=%v", ca.IsCA,
			ca.PermittedDNSDomainsCritical, ca.PermittedDNSDomains)
	}
	if ca.NotAfter.Sub(ca.NotBefore) > 25*time.Hour {
		t.Errorf("the authority lives %v", ca.NotAfter.Sub(ca.NotBefore))
	}
	if _, err := a.leaf("github.com"); err == nil {
		t.Error("the authority signed a host it does not inspect")
	}
}

func certPool(c *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c)
	return p
}

// Upstream answers over HTTP/2, but the client spoke HTTP/1.1 to the proxy; relaying the upstream
// protocol's status line verbatim is a response a strict client refuses.
func TestEgressAnswersInTheProtocolTheClientSpoke(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "https://"))
	authority, err := NewAuthority([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	proxy := egressProxy(t, &Egress{
		Inspect: map[string][]string{"localhost": {"/copilot_internal/"}}, Authority: authority, Ports: []string{port},
		Upstream: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{
			RootCAs: certPool(upstream.Certificate()), ServerName: "example.com"}},
	})
	trusted := x509.NewCertPool()
	trusted.AppendCertsFromPEM(authority.CertPEM())
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: trusted},
	}}
	resp, err := client.Get("https://localhost:" + port + "/copilot_internal/user")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Proto != "HTTP/1.1" {
		t.Fatalf("answered %d over %s, want 200 over HTTP/1.1", resp.StatusCode, resp.Proto)
	}
}
