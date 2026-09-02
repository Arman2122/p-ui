package controller

import (
	"os"
	"strings"
	"testing"
)

/*
The `protocol` bind rule snapshots the served kinds at start, so no stub
kind can reach the add handler in a test; the gate itself is driven in the
service package. This pins that the handler asks it before AddInbound, the
way the census placed it: on the API path, not in the service. Feature gap O4.
*/
func TestAddInboundHandlerAsksTheCorePreflight(t *testing.T) {
	body, err := os.ReadFile("inbound.go")
	if err != nil {
		t.Fatalf("read inbound.go: %v", err)
	}
	src := string(body)
	gate := strings.Index(src, "service.RefuseUnrunnableCore(inbound)")
	add := strings.Index(src, "a.inboundService.AddInbound(inbound)")
	if gate < 0 || add < 0 || gate > add {
		t.Fatalf("addInbound must ask RefuseUnrunnableCore before AddInbound (gate at %d, add at %d)", gate, add)
	}
}
