package localrpc

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixTransportAuthenticatesBothPeers(t *testing.T) {
	dir, err := os.MkdirTemp("", "node-peer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	for _, scenario := range []struct {
		name                 string
		serverUID, clientUID uint32
		success              bool
	}{
		{"authorized", uint32(os.Geteuid()), uint32(os.Geteuid()), true},
		{"wrong server", uint32(os.Geteuid()) + 1, uint32(os.Geteuid()), false},
		{"wrong client", uint32(os.Geteuid()), uint32(os.Geteuid()) + 1, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := filepath.Join(dir, "control.sock")
			listener, err := Listen(t.Context(), path, scenario.clientUID)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), ReadHeaderTimeout: time.Second}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() { _ = server.Close(); <-done }()
			client, err := Client(path, scenario.serverUID)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			client.Timeout = time.Second
			response, err := client.Get("http://runtime/probe")
			if response != nil {
				response.Body.Close()
			}
			if scenario.success {
				if err != nil || response.StatusCode != http.StatusNoContent {
					t.Fatalf("authenticated request=%v,%v", response, err)
				}
			} else if err == nil {
				t.Fatal("unauthorized Unix peer reached handler")
			}
		})
	}
	path := filepath.Join(dir, "occupied")
	if err := os.WriteFile(path, []byte("unmanaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(t.Context(), path, uint32(os.Geteuid())); !errors.Is(err, ErrPeer) {
		t.Fatalf("unmanaged path replacement=%v", err)
	}
}
