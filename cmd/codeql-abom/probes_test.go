package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/serve"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func answers(addr, path string) int {
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The probe listener runs beside the interface's and stops with it: a kubelet that probes a port
// nothing listens on restarts a healthy server.
func TestTheProbeListenerServesBesideTheInterfaceAndStopsWithIt(t *testing.T) {
	cfg := serveConfig{addr: freeAddr(t), probeAddr: freeAddr(t)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveWithProbes(ctx, cfg, &serve.Server{}, io.Discard) }()

	deadline := time.Now().Add(10 * time.Second)
	for answers(cfg.probeAddr, "/healthz") != http.StatusOK || answers(cfg.addr, "/healthz") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("neither listener came up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := answers(cfg.probeAddr, "/api/summary"); code != http.StatusNotFound {
		t.Errorf("the probe port served the API: %d", code)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("stopping returned %v", err)
	}
	if code := answers(cfg.probeAddr, "/healthz"); code != 0 {
		t.Errorf("the probe port still answers %d after the server stopped", code)
	}
}

// A probe address already taken is a server that cannot be probed, which is a failure to start, not
// a warning: the kubelet would otherwise kill it for an answer it never had a port to give.
func TestAProbeAddressInUseStopsTheServer(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := serveConfig{addr: freeAddr(t), probeAddr: taken.Addr().String()}
	done := make(chan error, 1)
	go func() { done <- serveWithProbes(context.Background(), cfg, &serve.Server{}, io.Discard) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a probe address in use returned no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server kept running without its probe listener")
	}
}
