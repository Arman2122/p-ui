package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The boot migration that clears an XTLS Vision flow an inbound cannot carry
loaded VLESS rows only. Its strip half is deliberately protocol-agnostic and
its own unit test covers a VMess row -- but the migration never handed it one,
so a VMess inbound stored with a flow kept handing out links that cannot
connect, across every restart, forever. F387.
*/
func TestVisionFlowMigrationClearsAStrandedFlowOnAVMessInbound(t *testing.T) {
	setupBulkDB(t)
	ib := mkInbound(t, 46001, model.VMESS,
		`{"clients":[{"id":"7d5a1c7e-2a1b-4c3d-9e8f-0a1b2c3d4e5f","email":"stranded@vmess","flow":"xtls-rprx-vision"}]}`)

	(&InboundService{}).MigrationRegateVisionFlow()

	var after model.Inbound
	if err := database.GetDB().First(&after, ib.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if strings.Contains(after.Settings, "xtls-rprx-vision") {
		t.Fatalf("the VMess inbound still carries a Vision flow after the migration:\n%s", after.Settings)
	}
}
