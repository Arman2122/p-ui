package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func wgDevice(protocol model.Protocol, port int, tag, cidr string) *model.Inbound {
	return &model.Inbound{
		Protocol: protocol, Port: port, Tag: tag, Enable: true,
		Settings: `{"address":["` + cidr + `"],"secretKey":"iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=","clients":[]}`,
	}
}

/*
Two host devices must never share a tunnel subnet, whichever kernel core made
them.

Both install the same connected route into the one host routing table, so a
client of one is answered over the other's device and encrypted to the wrong
peer -- traffic goes somewhere, to somebody else's tunnel, with nothing failing
loudly. AmneziaWG devices sit in that same table as kernel WireGuard's, so the
guard has to cover the pair and not just each protocol against itself.
*/
func TestAmneziawgAddressesCannotOverlapWireguards(t *testing.T) {
	initTestDB(t)
	service := &InboundService{}

	existing := wgDevice(model.WGKernel, 51888, "wg-in", "10.77.0.1/24")
	seedInbound(t, existing)

	for _, tc := range []struct {
		name       string
		candidate  *model.Inbound
		wantRefuse bool
	}{
		{"an AmneziaWG inbound on the same subnet", wgDevice(model.AWGKernel, 51830, "awg-in", "10.77.0.1/24"), true},
		{"an AmneziaWG inbound overlapping it", wgDevice(model.AWGKernel, 51831, "awg-in2", "10.77.0.0/16"), true},
		{"an AmneziaWG inbound on its own subnet", wgDevice(model.AWGKernel, 51832, "awg-in3", "10.88.0.1/24"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := service.checkWireguardAddressConflict(tc.candidate, 0)
			if tc.wantRefuse {
				if err == nil {
					t.Fatal("two kernel devices were allowed to share a subnet; one inbound's clients would be answered over the other's device")
				}
				if !strings.Contains(err.Error(), "overlaps") {
					t.Fatalf("refusal does not say what overlapped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("a distinct subnet must be allowed, got %v", err)
			}
		})
	}
}

// The guard is registry-driven, so a kernel core missing from it is a core whose
// addresses collide silently. This is the assertion that catches the next one.
func TestEveryKernelDeviceProtocolIsGuarded(t *testing.T) {
	protocols := hostWireguardProtocols()
	for _, want := range []string{"wgkernel", "awgkernel"} {
		found := false
		for _, got := range protocols {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s owns a host device but is not in the address-conflict guard: %v", want, protocols)
		}
	}
}
