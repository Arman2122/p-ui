package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

/*
The split-download override, saved and read back.

model.Host had the column and the subscription renderer read it, but the only
write path — HostController -> entity.HostGroup -> buildHostRows — had no field
for it at all. The JSON key the form sends was dropped by the binder and the row
was built without it, so the column stayed "" for every host anyone ever created.
The operator filled the tab in, got a success toast, reopened the form to an
empty tab, and every subscription kept handing out the inbound's original
download endpoint — the exact mismatch the feature exists to fix.
*/
func TestHostGroupRoundTripsTheDownloadOverride(t *testing.T) {
	setupBulkDB(t)
	svc := &HostService{}
	ib := mkInbound(t, 8443, model.VLESS, `{"clients":[]}`)

	const override = `{"address":"dl.example.com","port":443}`
	created, err := svc.AddHostGroup(&entity.HostGroup{
		InboundIds:       []int{ib.Id},
		Remark:           "cdn-front",
		Hosts:            []string{"up.example.com"},
		Port:             443,
		DownloadSettings: override,
	})
	if err != nil {
		t.Fatalf("AddHostGroup: %v", err)
	}

	var stored model.Host
	if err := database.GetDB().Where("group_id = ?", created[0].GroupId).First(&stored).Error; err != nil {
		t.Fatalf("read the stored host: %v", err)
	}
	if stored.DownloadSettings != override {
		t.Fatalf("download_settings = %q, want %q: the subscription reads this column and it was never written",
			stored.DownloadSettings, override)
	}

	// And back out, because the form reopening empty is how the operator finds out.
	group, err := svc.GetHostGroup(created[0].GroupId)
	if err != nil {
		t.Fatalf("GetHostGroup: %v", err)
	}
	if group.DownloadSettings != override {
		t.Fatalf("the group reads back %q, want %q: the form would reopen empty",
			group.DownloadSettings, override)
	}
}
