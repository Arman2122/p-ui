package xray

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/util/json_util"
)

// Replacing a hysteria handler closes the UDP listener its QUIC sessions share,
// and clients get no reset: they stall until their own idle timeout expires.
func TestComputeHotDiff_HysteriaClientOnlyChangeKeepsListener(t *testing.T) {
	stream := json_util.RawMessage(`{"network":"hysteria","security":"tls","hysteriaSettings":{"version":2}}`)
	oldCfg := makeHotConfig()
	oldCfg.InboundConfigs[1].Protocol = "hysteria"
	oldCfg.InboundConfigs[1].StreamSettings = stream
	oldCfg.InboundConfigs[1].Settings = json_util.RawMessage(`{"version":2,"clients":[{"email":"a","auth":"auth-a"}]}`)
	newCfg := makeHotConfig()
	newCfg.InboundConfigs[1].Protocol = "hysteria"
	newCfg.InboundConfigs[1].StreamSettings = stream
	newCfg.InboundConfigs[1].Settings = json_util.RawMessage(`{"version":2,"clients":[{"email":"a","auth":"auth-a"},{"email":"b","auth":"auth-b"}]}`)

	diff, ok := ComputeHotDiff(oldCfg, newCfg)
	if !ok {
		t.Fatal("client-only change must be hot-appliable")
	}
	if len(diff.RemovedInboundTags) != 0 || len(diff.AddedInbounds) != 0 {
		t.Fatalf("hysteria client-only change must not replace the handler, got removed=%v added=%d", diff.RemovedInboundTags, len(diff.AddedInbounds))
	}
	if len(diff.RemovedUsers) != 0 || len(diff.AddedUsers) != 1 || diff.AddedUsers[0].Email != "b" || diff.AddedUsers[0].Protocol != "hysteria" {
		t.Fatalf("expected a single AddUser op for b, got %+v", diff)
	}
}
