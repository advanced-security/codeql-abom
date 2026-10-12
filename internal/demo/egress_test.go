package demo

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
