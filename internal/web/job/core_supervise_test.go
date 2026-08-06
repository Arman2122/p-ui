package job

import (
	"context"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
recordingCore is a core the panel has never heard of by name: it claims one kind
and records what it is asked to converge on. Supervision that dispatches through
the registry reaches it; supervision written per core does not.
*/
type recordingCore struct {
	id         core.Kind
	kind       core.Kind
	reconciled [][]core.Instance
}

func (c *recordingCore) Kinds() []core.Kind { return []core.Kind{c.kind} }

func (c *recordingCore) Describe() core.Descriptor {
	return core.Descriptor{
		ID: c.id,
		Caps: core.Capabilities{
			UserHotAdd: core.No(), PerUserStats: core.No(), QuotaPushdown: core.No(),
			OnlineUsers: core.No(), ShareLink: core.No(),
		},
	}
}

func (c *recordingCore) Preflight(context.Context) error { return nil }

func (c *recordingCore) Reconcile(_ context.Context, desired []core.Instance) error {
	c.reconciled = append(c.reconciled, desired)
	return nil
}

func (c *recordingCore) StopAll(context.Context) error { return nil }

/*
The defect this pins: supervision named one core, so any other core got no
reconcile — no convergence at boot, no self-heal after a crash — until someone
wrote it a job of its own. A registered core must be supervised by being
registered, and the one core the panel converges itself must be left alone.
*/
func TestCoreSuperviseJobReconcilesEveryRegisteredCore(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())

	seedLocalInbound(t, "inbound-vless-supervised", model.VLESS, 44301,
		`{"clients":[{"id":"3f1d6b1e-0d2a-4a3a-9f43-2b4d5a6c7e80","email":"served@vless","enable":true}]}`)

	served := &recordingCore{id: "recording", kind: core.Kind(model.VLESS)}
	panelBuilt := &recordingCore{id: "panel-built", kind: core.Kind(model.Trojan)}
	registry := core.NewRegistry()
	for _, registered := range []*recordingCore{served, panelBuilt} {
		if err := registry.Register(registered); err != nil {
			t.Fatalf("register %s: %v", registered.id, err)
		}
	}
	Cores = registry
	t.Cleanup(func() { Cores = nil })

	NewCoreSuperviseJob(panelBuilt.id).Run()

	if len(served.reconciled) != 1 {
		t.Fatalf("registered core was reconciled %d times, want exactly 1 — supervision never reached it through the registry", len(served.reconciled))
	}
	desired := served.reconciled[0]
	if len(desired) != 1 {
		t.Fatalf("desired = %v, want the one enabled local inbound of the core's kind", desired)
	}
	if desired[0].Tag != "inbound-vless-supervised" {
		t.Errorf("desired[0].Tag = %q, want %q", desired[0].Tag, "inbound-vless-supervised")
	}
	if len(desired[0].Users) != 1 || desired[0].Users[0].Email != "served@vless" {
		t.Errorf("desired[0].Users = %v, want the inbound's one enabled client", desired[0].Users)
	}
	if len(panelBuilt.reconciled) != 0 {
		t.Errorf("the core the panel converges itself was reconciled %d times, want 0 — an instance set cannot describe its config", len(panelBuilt.reconciled))
	}
}

/*
mtg meters per secret, so an inbound whose clients are connected but idle moves
no bytes — and the per-inbound online view only shows a client on tags that did.
Nothing else marks an mtproto tag active, so without this the whole inbound goes
dark ~20s (the online grace) after its last byte.
*/
func TestCoreSuperviseJobKeepsIdleSidecarTagsActive(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())

	seedLocalInbound(t, "inbound-mtproto-idle", model.MTProto, 44300,
		`{"clients":[{"email":"idle@mtproto","secret":"ee00112233445566778899aabbccddeeff","enable":true}]}`)

	registry, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("build the core registry: %v", err)
	}
	Cores = registry
	t.Cleanup(func() { Cores = nil })

	process := xray.NewProcess(&xray.Config{})
	xray.GetManager().Replace(process)
	t.Cleanup(func() { xray.GetManager().Replace(nil) })

	NewCoreSuperviseJob(cores.PanelConvergedCore).Run()

	active := process.GetLocalActiveInbounds()
	if !slices.Contains(active, "inbound-mtproto-idle") {
		t.Fatalf("localActiveInbounds = %v, want it to contain the idle sidecar's tag", active)
	}
	if online := process.GetLocalOnlineClients(); len(online) != 0 {
		t.Errorf("localOnlineClients = %v, want none — this job reports tags, not emails", online)
	}
}

func seedLocalInbound(t *testing.T, tag string, protocol model.Protocol, port int, settings string) {
	t.Helper()
	inbound := &model.Inbound{
		UserId:   1,
		Enable:   true,
		Tag:      tag,
		Port:     port,
		Protocol: protocol,
		Settings: settings,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed %s inbound: %v", protocol, err)
	}
}
