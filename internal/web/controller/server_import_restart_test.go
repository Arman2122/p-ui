package controller

import (
	"os"
	"strings"
	"testing"
)

/*
A browser restarts the panel itself after a database restore; a token caller
has no next step, so the panel kept serving the state loaded before the
restore. The handler now restarts server-side for a token caller, after the
answer, and leaves the browser's own restart alone. Feature gap O10.
*/
func TestImportDBRestartsThePanelForATokenCaller(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := string(body)
	start := strings.Index(src, "func (a *ServerController) importDB(")
	if start < 0 {
		t.Fatal("importDB handler not found")
	}
	handler := src[start:]
	if end := strings.Index(handler, "\nfunc "); end > 0 {
		handler = handler[:end]
	}
	answer := strings.Index(handler, "importDatabaseSuccess")
	gate := strings.Index(handler, `c.GetBool("api_authed")`)
	restart := strings.Index(handler, "a.panelService.RestartPanel(")
	if answer < 0 || gate < 0 || restart < 0 || gate < answer || restart < gate {
		t.Fatalf("importDB must answer, then restart only for a token caller (answer %d, gate %d, restart %d)", answer, gate, restart)
	}
}
