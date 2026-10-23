package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// A status probe must bound peer-controlled input before decoding JSON.
func TestProbeRejectsOversizedStatusBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":{"cpuPct":1,"panelVersion":"`))
		pad := strings.Repeat("x", 1<<20)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte(pad))
		}
		_, _ = w.Write([]byte(`"}}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	n := &model.Node{
		Id: 1, Name: "big", Scheme: "http", Address: u.Hostname(), Port: port,
		BasePath: "/", Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "skip",
	}

	svc := &NodeService{}
	if _, err := svc.Probe(context.Background(), n); err == nil || err.Error() != "unexpected EOF" {
		t.Fatalf("Probe error = %v, want unexpected EOF", err)
	}
}
