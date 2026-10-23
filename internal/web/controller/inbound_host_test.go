package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestResolveHostKeepsVisitorAddressOutOfLinks(t *testing.T) {
	initTestDB(t)
	if err := (&service.SettingService{}).SetTrustedProxyCIDRs("127.0.0.1/32"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		forwarded string
		want      string
	}{
		{"visitor IP only", "", "panel.example"},
		{"forwarded host", "proxy.example:8443", "proxy.example"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "https://panel.example:2053/panel/api/inbounds/allLinks", nil)
			c.Request.RemoteAddr = "127.0.0.1:12345"
			c.Request.Header.Set("X-Real-IP", "198.51.100.27")
			c.Request.Header.Set("X-Forwarded-Host", tt.forwarded)
			if got := resolveHost(c); got != tt.want {
				t.Fatalf("resolveHost = %q, want %q", got, tt.want)
			}
		})
	}
}
