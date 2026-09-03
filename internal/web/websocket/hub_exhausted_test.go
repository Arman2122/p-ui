package websocket

import (
	"os"
	"strings"
	"testing"
)

/*
After its restart attempts ran out the hub logged and returned, leaving
every browser holding a socket nobody served: the frontend polls only once
the socket closes, so those tabs went silent for good. Run now closes the
sockets on the way out. The crash loop cannot be driven from a test, so
this pins the call; shutdown itself is covered by TestHub_StopClosesAllClients.
Feature gap F1.
*/
func TestRunClosesEverySocketAfterExhaustingRestarts(t *testing.T) {
	body, err := os.ReadFile("hub.go")
	if err != nil {
		t.Fatalf("read hub.go: %v", err)
	}
	src := string(body)
	run := strings.Index(src, "func (h *Hub) Run() {")
	if run < 0 {
		t.Fatal("Run not found")
	}
	fn := src[run:]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}
	shutdown := strings.Index(fn, "h.shutdown()")
	exhausted := strings.Index(fn, "exhausting restart attempts")
	if shutdown < 0 || exhausted < 0 {
		t.Fatalf("Run must close every socket when it gives up (shutdown at %d, log at %d)", shutdown, exhausted)
	}
}
