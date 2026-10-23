package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

type restoreBeforeQuotaCore struct {
	*proxyCore
	restored bool
	reset    bool
}

func (c *restoreBeforeQuotaCore) AddUser(context.Context, core.Instance, core.User) error {
	c.restored = true
	return nil
}

func (c *restoreBeforeQuotaCore) ResetQuota(context.Context, string) error {
	if c.restored {
		c.reset = true
	}
	return nil
}

func TestClientResetRestoresDaemonUserBeforeClearingQuota(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	fake := &restoreBeforeQuotaCore{proxyCore: newProxyCore(&declaredCore{id: "quota-order", kind: "vless"})}
	installCores(t, fake)
	ib := seedDepletableClient(t, 29025, "quota-order", "quota-order@x")
	if _, _, err := (&InboundService{}).AddTraffic(nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := readEnableLocations(t, ib.Id, "quota-order@x"); got.traffic {
		t.Fatal("fixture was not depleted")
	}
	if _, err := (&ClientService{}).ResetTrafficByEmail(&InboundService{}, "quota-order@x"); err != nil {
		t.Fatal(err)
	}
	if !fake.restored || !fake.reset {
		t.Fatalf("restored=%v effective quota reset=%v, want both true", fake.restored, fake.reset)
	}
}
