package xray

import (
	"net"
	"strings"
	"testing"
)

// listenLoopback occupies a loopback port and reports it, standing in for the
// other panel's Xray that already holds the API port.
func listenLoopback(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	return lis.Addr().(*net.TCPAddr).Port
}

/*
Start must refuse when another process already holds the API port.

Xray's SO_REUSEPORT makes the second bind succeed, after which the panel's gRPC
calls are split across two cores at random and per-user traffic silently stops
being billed. The assertion is on the message, not merely on err != nil: without
the check Start still fails on this box, but from exec'ing a missing binary, and
that failure proves nothing about the conflict.
*/
func TestStartRefusesAPIPortHeldByAnotherProcess(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	port := listenLoopback(t)

	p := newProcess(&Config{InboundConfigs: []InboundConfig{{Tag: "api", Port: port}}})
	err := p.Start()
	if err == nil {
		_ = p.Stop()
		t.Fatal("Start succeeded while another process held the api port; it must refuse")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("want an api-port-in-use error, got: %v", err)
	}
	if p.IsRunning() {
		t.Fatal("no xray process may be left running after a refused start")
	}
}

// A free API port must not be mistaken for a conflict, or every start breaks.
func TestCheckAPIPortFreeAllowsUnusedPort(t *testing.T) {
	held := listenLoopback(t)
	// Bind then close, so the number names a port nothing is listening on.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	free := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()

	if err := checkAPIPortFree(free); err != nil {
		t.Fatalf("free port %d reported as in use: %v", free, err)
	}
	if err := checkAPIPortFree(0); err != nil {
		t.Fatalf("a config with no api inbound must start: %v", err)
	}
	if err := checkAPIPortFree(held); err == nil {
		t.Fatalf("held port %d reported as free", held)
	}
}

func TestAPIPortOf(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   int
	}{
		{"nil config", nil, 0},
		{"no api inbound", &Config{InboundConfigs: []InboundConfig{{Tag: "in", Port: 443}}}, 0},
		{"api inbound", &Config{InboundConfigs: []InboundConfig{{Tag: "in", Port: 443}, {Tag: "api", Port: 62789}}}, 62789},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apiPortOf(tt.config); got != tt.want {
				t.Fatalf("apiPortOf = %d, want %d", got, tt.want)
			}
		})
	}
}
