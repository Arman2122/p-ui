package job

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
mtg meters per secret, so an inbound whose clients are connected but idle moves
no bytes — and the per-inbound online view only shows a client on tags that did.
Nothing else marks an mtproto tag active, so without this the whole inbound goes
dark ~20s (the online grace) after its last byte.
*/
func TestMtprotoJobKeepsIdleSidecarTagsActive(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())

	seedIdleMtprotoInbound(t, "inbound-mtproto-idle")

	process := xray.NewProcess(&xray.Config{})
	xray.GetManager().Replace(process)
	t.Cleanup(func() { xray.GetManager().Replace(nil) })

	NewMtprotoJob().Run()

	active := process.GetLocalActiveInbounds()
	if !slices.Contains(active, "inbound-mtproto-idle") {
		t.Fatalf("localActiveInbounds = %v, want it to contain the idle sidecar's tag", active)
	}
	if online := process.GetLocalOnlineClients(); len(online) != 0 {
		t.Errorf("localOnlineClients = %v, want none — this job reports tags, not emails", online)
	}
}

func seedIdleMtprotoInbound(t *testing.T, tag string) {
	t.Helper()
	inbound := &model.Inbound{
		UserId:   1,
		Enable:   true,
		Tag:      tag,
		Port:     44300,
		Protocol: model.MTProto,
		Settings: `{"clients":[{"email":"idle@mtproto","secret":"ee00112233445566778899aabbccddeeff","enable":true}]}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed mtproto inbound: %v", err)
	}
}
