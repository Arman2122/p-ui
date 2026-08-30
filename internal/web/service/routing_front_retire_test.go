package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Viewing the config must not retire a front the running core still uses.

`GetXrayConfig` is named like a getter and reads like one, but the compile it
runs ends in `reapFronts`, which DELETES every panel-owned egress row no current
rule names -- and EgressService.Del tears down the `ip rule`, the routing table
and the blackhole that go with it.

Two callers reach it without applying anything: the "view config" button, and
the pre-flight that checks whether a newly downloaded Xray binary can load the
config. So opening a settings page could delete kernel state the RUNNING core is
still forwarding through, and the update pre-flight could do it to a panel that
is mid-upgrade.

Driven through GetConfigJson rather than GetXrayConfig on purpose: its signature
does not change, so reverting the fix leaves this test compiling and RED. A test
written against GetXrayConfig(false) would fail to compile instead, which
certifies nothing. F054.
*/
func TestViewingTheConfigDoesNotRetireAFront(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	db := database.GetDB()

	// An inbound, and a panel-owned front for it that no routing rule names --
	// exactly the row reapFronts exists to collect.
	ib := &model.Inbound{
		Tag: "tun-in", Enable: true, Listen: "0.0.0.0", Port: 34100,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: "front for tun-in", IngressInboundId: &ib.Id,
	}
	if err := db.Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}

	if _, err := (&ServerService{}).GetConfigJson(); err != nil {
		t.Fatalf("GetConfigJson: %v", err)
	}

	var count int64
	if err := database.GetDB().Model(&model.Egress{}).Where("id = ?", front.Id).Count(&count).Error; err != nil {
		t.Fatalf("re-count the front: %v", err)
	}
	if count == 0 {
		t.Fatal("viewing the config deleted a panel-owned egress front; EgressService.Del also removes its ip rule, routing table and blackhole, so a read tore down kernel state the running core is still forwarding through")
	}
}
