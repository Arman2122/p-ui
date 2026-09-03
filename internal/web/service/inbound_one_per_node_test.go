package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A host-daemon core (strongSwan owns UDP 500/4500 for the whole host) can serve
one inbound per node, and nothing in the contract could say so: a second IKEv2
inbound would have been accepted and then fought the first for the daemon.
The descriptor now declares OnePerNode and the add gate counts. Roster contract
gap, one-inbound-per-node refusal shape.
*/
func TestRefuseUnrunnableCoreKeepsAHostDaemonToOneInbound(t *testing.T) {
	initTestDB(t)
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := stubCore{
		descriptor: core.Descriptor{ID: "hostdaemon", TitleKey: "cores.stub.title", OnePerNode: true},
		kinds:      []core.Kind{"hostkind"},
	}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)

	first := &model.Inbound{UserId: 1, Protocol: model.Protocol("hostkind"), Tag: "host-1", Port: 500, Settings: "{}", StreamSettings: "{}"}
	if err := RefuseUnrunnableCore(first); err != nil {
		t.Fatalf("the first inbound of a host daemon must pass, got %v", err)
	}
	if err := database.GetDB().Create(first).Error; err != nil {
		t.Fatalf("seed the first inbound: %v", err)
	}

	second := &model.Inbound{Protocol: model.Protocol("hostkind"), Tag: "host-2"}
	err = RefuseUnrunnableCore(second)
	if err == nil || !strings.Contains(err.Error(), "already runs a hostkind inbound") {
		t.Fatalf("second inbound of a host daemon: err = %v, want the one-per-node refusal", err)
	}
	if err := RefuseUnrunnableCore(first); err != nil {
		t.Fatalf("re-checking the existing inbound must not count itself, got %v", err)
	}
	node := 3
	if err := RefuseUnrunnableCore(&model.Inbound{Protocol: model.Protocol("hostkind"), NodeID: &node}); err != nil {
		t.Fatalf("a node-hosted inbound is the node's business, got %v", err)
	}
}
