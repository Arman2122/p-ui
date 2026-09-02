package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An export dropped every external link, so a client rebuilt from it on
another panel lost the mirror subscriptions and hand-added links its
operator had attached. Links now ride the payload and import writes them
back. Feature gap C3.
*/
func TestClientExportRoundTripsExternalLinks(t *testing.T) {
	setupBulkDB(t)
	ib := seedBulkClients(t, 23923, "lnk@x")
	svc := &ClientService{}
	want := []ExternalLinkInput{{Kind: "subscription", Value: "https://mirror.example.com/sub/abc", Remark: "mirror"}}
	if err := svc.SetExternalLinksByEmail("lnk@x", want); err != nil {
		t.Fatalf("attach links: %v", err)
	}

	items, err := svc.ExportEmails([]string{"lnk@x"})
	if err != nil {
		t.Fatalf("ExportEmails: %v", err)
	}
	if len(items) != 1 || len(items[0].ExternalLinks) != 1 || items[0].ExternalLinks[0] != want[0] {
		t.Fatalf("exported links = %+v, want %+v", items, want)
	}

	if _, _, err := svc.BulkDelete(&InboundService{}, []string{"lnk@x"}, false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	items[0].InboundIds = []int{ib.Id}
	res, _, err := svc.ImportClients(&InboundService{}, items)
	if err != nil {
		t.Fatalf("ImportClients: %v", err)
	}
	if res.Created != 1 || len(res.Skipped) != 0 {
		t.Fatalf("import created=%d skipped=%+v, want the client back cleanly", res.Created, res.Skipped)
	}
	var rec model.ClientRecord
	if err := database.GetDB().Where("email = ?", "lnk@x").First(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := svc.GetExternalLinksForRecord(rec.Id)
	if err != nil {
		t.Fatalf("links after import: %v", err)
	}
	if len(got) != 1 || got[0].Kind != want[0].Kind || got[0].Value != want[0].Value || got[0].Remark != want[0].Remark {
		t.Fatalf("links after import = %+v, want %+v", got, want)
	}
}
