package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
)

func TestInboundCounterResetPreservesClientDaemonQuota(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	recorder := &quotaRecorder{proxyCore: newProxyCore(&declaredCore{id: "proxy", kind: "vless"})}
	installCores(t, recorder)
	inbound := seedDepletableClient(t, 29024, "in-quota-preserve", "quota-preserve@x")
	var before core.ClientTraffic
	if err := database.GetDB().Where("email = ?", "quota-preserve@x").First(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before.Up+before.Down == 0 {
		t.Fatal("fixture has no client usage")
	}
	if err := (&InboundService{}).ResetAllTraffics(); err != nil {
		t.Fatal(err)
	}
	if got := recorder.resets; len(got) != 0 {
		t.Fatalf("inbound-only reset cleared daemon quotas %v", got)
	}
	var after core.ClientTraffic
	if err := database.GetDB().Where("email = ?", before.Email).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Up != before.Up || after.Down != before.Down {
		t.Fatalf("client counters = %d/%d, want %d/%d", after.Up, after.Down, before.Up, before.Down)
	}
	if err := database.GetDB().First(inbound, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if inbound.Up != 0 || inbound.Down != 0 {
		t.Fatalf("inbound counters = %d/%d, want 0/0", inbound.Up, inbound.Down)
	}
}
