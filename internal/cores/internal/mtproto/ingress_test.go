package mtproto

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The sidecar terminates MTProto itself, so without the bridge Xray's router never
sees a packet and a rule naming this inbound could never match. The editor used
to offer that tag anyway; now the core says why it cannot.
*/
func TestUnbridgedMtprotoIsBlockedWithAReason(t *testing.T) {
	for _, tc := range []struct {
		name      string
		settings  string
		wantTag   string
		wantBlock string
	}{
		{"bridged answers with its tag", `{"routeThroughXray":true,"routeXrayPort":2398}`, "mt-1", ""},
		{"the switch is off", `{"routeThroughXray":false}`, "", blockedBridgeOff},
		{"bridged but no port is not a bridge", `{"routeThroughXray":true,"routeXrayPort":0}`, "", blockedBridgeOff},
		{"unreadable settings fail closed", `{`, "", blockedBridgeOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&Core{}).IngressHandle(context.Background(), core.Instance{
				ID: 1, Kind: Kind, Tag: "mt-1", Settings: tc.settings,
			})
			if err != nil {
				t.Fatalf("IngressHandle: %v", err)
			}
			if got.Tag != tc.wantTag {
				t.Errorf("Tag = %q, want %q", got.Tag, tc.wantTag)
			}
			if got.BlockedKey != tc.wantBlock {
				t.Errorf("BlockedKey = %q, want %q", got.BlockedKey, tc.wantBlock)
			}
			if got.Device != "" {
				t.Errorf("Device = %q, want empty: an mtproto bridge is a tag, never a device", got.Device)
			}
		})
	}
}

func TestMtprotoIsAnInternalIngress(t *testing.T) {
	c := &Core{}
	if got := c.IngressSelector(Kind); got != core.IngressInternal {
		t.Errorf("IngressSelector(%q) = %q, want %q", Kind, got, core.IngressInternal)
	}
	if got := c.IngressSelector("vless"); got != core.IngressNone {
		t.Errorf("IngressSelector of a kind this core does not serve = %q, want %q", got, core.IngressNone)
	}
}
