package sub

import (
	"os"
	"strings"
	"testing"
)

// The listener must hand handshakes the pair on disk, not a pair captured at
// start: that is what lets an ACME renewal land without a restart.
func TestTLSListenerServesTheReloadedPair(t *testing.T) {
	body, err := os.ReadFile("sub.go")
	if err != nil {
		t.Fatalf("read sub.go: %v", err)
	}
	src := string(body)
	if !strings.Contains(src, "GetCertificate: crypto.NewCertReloader(certFile, keyFile, cert).GetCertificate") {
		t.Fatal("the TLS listener does not serve certificates through the reloader")
	}
	if strings.Contains(src, "Certificates: []tls.Certificate{cert}") {
		t.Fatal("the TLS listener still pins the pair loaded at start")
	}
}
