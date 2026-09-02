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
no module could be created and then never start. AddInbound now asks the
owning core's Preflight and refuses with its reason. Feature gap O4.
*/
func TestAddInboundRefusesACoreThisHostCannotRun(t *testing.T) {
	initTestDB(t)
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

	inbound := &model.Inbound{Protocol: model.Protocol("stubkind"), Tag: "stub-1", Remark: "stub", Port: 46012, Settings: "{}", StreamSettings: "{}"}
	_, _, _, err = (&InboundService{}).AddInbound(inbound)
	if err == nil || !strings.Contains(err.Error(), "kernel module missing") {
		t.Fatalf("AddInbound for an unusable core: err = %v, want the core's own reason", err)
	}
}
