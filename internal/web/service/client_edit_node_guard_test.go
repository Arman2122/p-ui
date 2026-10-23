package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

type nodeDirtyProbe struct {
	fakeNodeRuntime
	watch   int
	probed  bool
	isDirty bool
}

func (p *nodeDirtyProbe) UpdateUser(ctx context.Context, ib *model.Inbound, email string, client model.Client) error {
	var node model.Node
	if err := database.GetDB().First(&node, p.watch).Error; err != nil {
		return err
	}
	p.probed, p.isDirty = true, node.ConfigDirty
	return p.fakeNodeRuntime.UpdateUser(ctx, ib, email, client)
}

func TestClientEditMarksExcludedNodeBeforeApplying(t *testing.T) {
	setupBulkDB(t)
	previous := runtime.GetManager()
	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(previous) })
	probe := &nodeDirtyProbe{}
	var inbounds []int
	for i := range 2 {
		node := &model.Node{Name: fmt.Sprintf("guard-%d", i), Address: "127.0.0.1", Port: 2096 + i, ApiToken: "tok", Enable: true, Status: "online"}
		if err := database.GetDB().Create(node).Error; err != nil {
			t.Fatal(err)
		}
		ib := mkInbound(t, 47400+i, model.VLESS, `{"clients":[]}`)
		if err := database.GetDB().Model(ib).Update("node_id", node.Id).Error; err != nil {
			t.Fatal(err)
		}
		inbounds = append(inbounds, ib.Id)
		if i == 0 {
			mgr.SetRuntimeOverride(node.Id, probe)
		} else {
			probe.watch = node.Id
			mgr.SetRuntimeOverride(node.Id, &fakeNodeRuntime{})
		}
	}
	svc, inboundSvc := &ClientService{}, &InboundService{}
	client := model.Client{Email: "before@guard", ID: "11111111-1111-1111-1111-111111111111", SubID: "guard-sub-id", Enable: true}
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{Client: client, InboundIds: inbounds}); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&model.Node{}).Where("id > 0").Update("config_dirty", false).Error; err != nil {
		t.Fatal(err)
	}
	record := lookupClientRecord(t, client.Email)
	client.Email = "after@guard"
	if _, err := svc.Update(inboundSvc, record.Id, client, inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if !probe.probed || !probe.isDirty {
		t.Fatalf("other node at first push: observed=%v dirty=%v, want both true", probe.probed, probe.isDirty)
	}
}
