package egress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// anywhere dials whatever address a name resolves to, as these tests' targets are on loopback.
func anywhere(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func egressProxy(t *testing.T, e *Proxy) *url.URL {
	t.Helper()
	if e.Dial == nil {
		e.Dial = anywhere
	}
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
	proxy := egressProxy(t, &Proxy{Allow: []string{"localhost"}, Ports: []string{port}, Log: &log})
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
	proxy := egressProxy(t, &Proxy{Allow: []string{"api.githubcopilot.com"}})
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
	e := &Proxy{Allow: []string{"api.githubcopilot.com", ".individual.githubcopilot.com"}}
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
	e := &Proxy{
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
	proxy := egressProxy(t, &Proxy{
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

// An HTTP/2 answer streamed without a length is relayed as one that ends: written to be read until
// the connection closes, on a connection the proxy keeps open, a client waits on it forever.
func TestEgressRelaysAnAnswerOfUnknownLengthSoItEnds(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("part one,"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("part two"))
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "https://"))
	authority, err := NewAuthority([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	proxy := egressProxy(t, &Proxy{
		Inspect: map[string][]string{"localhost": {"/copilot_internal/"}}, Authority: authority, Ports: []string{port},
		Upstream: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{
			RootCAs: certPool(upstream.Certificate()), ServerName: "example.com"}},
	})
	trusted := x509.NewCertPool()
	trusted.AppendCertsFromPEM(authority.CertPEM())
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: trusted},
	}}
	for i := range 2 {
		resp, err := client.Get("https://localhost:" + port + "/copilot_internal/user")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "part one,part two" {
			t.Fatalf("request %d read %q, %v", i, body, err)
		}
	}
}

// An allowed name is reached only at public addresses, so a name an allowed suffix covers cannot be
// pointed inside the cluster the proxy runs in.
func TestTheProxyReachesAnAllowedNameOnlyAtPublicAddresses(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer target.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "https://"))
	var log bytes.Buffer
	e := &Proxy{Allow: []string{"localhost"}, Ports: []string{port}, Log: &log}
	srv := httptest.NewServer(e)
	defer srv.Close()
	proxy, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // never reached
	if resp, err := client.Get("https://localhost:" + port + "/"); err == nil {
		resp.Body.Close()
		t.Fatal("an allowed name was reached at a loopback address")
	}
	if !strings.Contains(log.String(), "not a public address") {
		t.Errorf("the refusal was not recorded as one: %q", log.String())
	}
}

func TestOnlyAnAddressOnThePublicInternetIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"140.82.112.3": true, "2606:50c0:8000::153": true,
		"10.0.0.1": false, "172.16.0.1": false, "192.168.1.1": false, "127.0.0.1": false, "0.0.0.0": false,
		"169.254.169.254": false, "100.64.0.1": false, "198.18.0.1": false, "192.0.0.170": false,
		"224.0.0.1": false, "::1": false, "fd00::1": false, "fe80::1": false, "fd00:ec2::254": false,
		"::ffff:10.0.0.1": false, "::ffff:100.64.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v, want %v", addr, got, want)
		}
	}
}

// A proxy told which clients to answer answers no other.
func TestTheProxyAnswersOnlyTheClientsItAdmits(t *testing.T) {
	proxy := egressProxy(t, &Proxy{Allow: []string{"api.githubcopilot.com"},
		Admit: func(string) bool { return false }})
	conn, err := net.Dial("tcp", proxy.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("CONNECT api.githubcopilot.com:443 HTTP/1.1\r\nHost: api.githubcopilot.com:443\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a client the proxy does not admit was answered %d", resp.StatusCode)
	}
	if !Loopback("127.0.0.1:1") || Loopback("10.0.0.1:1") || Loopback("nonsense") {
		t.Error("Loopback admits other than loopback clients")
	}
}

// One session's agent cannot hold every tunnel the proxy can open: past its bound a tunnel is refused,
// and one that closes makes room again.
func TestTheProxyHoldsNoMoreTunnelsThanItsBound(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c); _ = c.Close() }()
		}
	}()
	_, port, _ := net.SplitHostPort(target.Addr().String())
	proxy := egressProxy(t, &Proxy{Allow: []string{"localhost"}, Ports: []string{port}, MaxTunnels: 1})
	connect := func() (net.Conn, int) {
		conn, err := net.Dial("tcp", proxy.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write([]byte("CONNECT localhost:" + port + " HTTP/1.1\r\nHost: localhost:" + port + "\r\n\r\n"))
		// A tunnel's answer has no end: its body is the tunnel, closed with the connection.
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil) //nolint:bodyclose // closed with conn
		if err != nil {
			t.Fatal(err)
		}
		return conn, resp.StatusCode
	}
	first, code := connect()
	if code != http.StatusOK {
		t.Fatalf("the first tunnel answered %d", code)
	}
	second, code := connect()
	second.Close()
	if code != http.StatusServiceUnavailable {
		t.Errorf("a tunnel past the bound answered %d", code)
	}
	first.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		third, code := connect()
		third.Close()
		if code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a tunnel after the first closed still answered %d", code)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The chart names each pool's hosts, the authoring pool's among them; a host Copilot comes to need is
// added here, and the chart's listener for sessions must admit it too, or every session is refused it.
func TestTheChartsAuthoringListenerAdmitsTheHostsCopilotNeeds(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "infra", "k8s", "helm", "codeql-abom", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		EgressProxy struct {
			Allow map[string][]string `yaml:"allow"`
		} `yaml:"egressProxy"`
	}
	if err := yaml.Unmarshal(body, &values); err != nil {
		t.Fatal(err)
	}
	if got := values.EgressProxy.Allow["authoring"]; !slices.Equal(got, CopilotHosts) {
		t.Errorf("the chart's authoring listener admits %v; Copilot needs %v", got, CopilotHosts)
	}
}
