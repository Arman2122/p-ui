package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The inbound form refuses a kind whose core reports itself unusable on this
host, but the API path took it: an inbound for a kernel WireGuard core with
no module could be created and then never start. The add handler now asks
the owning core's Preflight through RefuseUnrunnableCore, which this drives
with a registry holding a core that says no; the service itself stays free
of host checks so its own tests run on a bare runner. Feature gap O4.
*/
func TestRefuseUnrunnableCoreNamesTheCoresReason(t *testing.T) {
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := stubCore{
		descriptor: core.Descriptor{ID: "stubcore", TitleKey: "cores.stub.title"},
		kinds:      []core.Kind{"stubkind"},
		unusable:   errors.New("kernel module missing"),
	}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)

	err = RefuseUnrunnableCore(&model.Inbound{Protocol: model.Protocol("stubkind"), Tag: "stub-1"})
	if err == nil || !strings.Contains(err.Error(), "kernel module missing") || !strings.Contains(err.Error(), "stubkind") {
		t.Fatalf("unusable core: err = %v, want the kind and the core's own reason", err)
	}
	node := 7
	if err := RefuseUnrunnableCore(&model.Inbound{Protocol: model.Protocol("stubkind"), NodeID: &node}); err != nil {
		t.Fatalf("a node-hosted inbound is the node's business, got %v", err)
	}
	if err := RefuseUnrunnableCore(&model.Inbound{Protocol: model.VLESS}); err != nil {
		t.Fatalf("an Xray kind keeps its own -test pre-flight, got %v", err)
	}
}
