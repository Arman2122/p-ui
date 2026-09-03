package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An AmneziaWG inbound the kernel would refuse must be refused at the save.

awg.Params.Validate mirrors src/netlink.c rule for rule, and it was reached only
when the device was configured. The module answers a failed check with EINVAL
and a ratelimited dmesg line the operator never sees — so the panel stored the
row, reported success, and the inbound quietly did not work, while the .conf
handed to clients described the parameters that were asked for.
F267 / F104 / F171 / F190.

Asked of the CORE that owns the kind, not decided here: the service layer has no
business knowing what an obfuscation parameter means, and the dispatch ratchet
is right to refuse a second place that does.
*/
func TestAnInboundTheKernelWouldRefuseIsRefusedAtTheSave(t *testing.T) {
	initTestDB(t)

	cases := []struct {
		name     string
		settings string
		refused  bool
		why      string
	}{
		{
			name:     "junk range inverted",
			settings: `{"secretKey":"","peers":[],"awg":{"jmin":100,"jmax":10}}`,
			refused:  true,
			why:      "jmin above jmax is refused by netlink.c",
		},
		{
			name:     "two headers covering the same value",
			settings: `{"secretKey":"","peers":[],"awg":{"h1":5,"h2":5}}`,
			refused:  true,
			why:      "overlapping headers make one message type indistinguishable from another",
		},
		{
			name:     "a header-protection key of the wrong length",
			settings: `{"secretKey":"","peers":[],"awg":{"headerProtectionKey":"c2hvcnQ="}}`,
			refused:  true,
			why:      "the module requires an exact key size",
		},
		{
			name:     "no obfuscation at all is a plain kernel WireGuard device",
			settings: `{"secretKey":"","peers":[]}`,
			refused:  false,
			why:      "an inbound with no awg block is valid",
		},
		{
			name:     "a coherent parameter set",
			settings: `{"secretKey":"","peers":[],"awg":{"jc":4,"jmin":10,"jmax":50}}`,
			refused:  false,
			why:      "nothing here contradicts the module's rules",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateInstanceForKind(&model.Inbound{
				Protocol: model.AWGKernel,
				Settings: c.settings,
			})
			if c.refused && err == nil {
				t.Fatalf("accepted a configuration the kernel refuses: %s", c.why)
			}
			if !c.refused && err != nil {
				t.Fatalf("refused a valid configuration (%s): %v", c.why, err)
			}
		})
	}
}

// A kind whose core declares no validator is saved unvalidated, exactly as every
// kind was before. This adds a refusal where one is available; it does not
// invent a policy for kinds that have none.
func TestAKindWithNoValidatorIsUnaffected(t *testing.T) {
	initTestDB(t)

	for _, protocol := range []model.Protocol{model.VLESS, model.Mixed, model.Protocol("ftp")} {
		if err := validateInstanceForKind(&model.Inbound{
			Protocol: protocol,
			Settings: `{"anything":"at all"}`,
		}); err != nil {
			t.Fatalf("%s gained a refusal it never had: %v", protocol, err)
		}
	}
}

/*
And the refusal reaches the save path, not just the helper.

The form, the REST API and /inbounds/import all land in AddInbound and
UpdateInbound, which is why the call sits there rather than in a validator the
UI happens to run.
*/
func TestAddInboundRefusesObfuscationTheModuleWouldReject(t *testing.T) {
	initTestDB(t)

	_, _, _, err := (&InboundService{}).AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Remark: "bad-awg", Tag: "bad-awg-1",
		Listen: "0.0.0.0", Port: 25201, Protocol: model.AWGKernel,
		Settings: `{"secretKey":"","peers":[],"awg":{"jmin":100,"jmax":10}}`,
	})
	if err == nil {
		t.Fatal("AddInbound stored an AmneziaWG inbound the module will refuse to configure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "junk") &&
		!strings.Contains(strings.ToLower(err.Error()), "jmin") {
		t.Fatalf("the refusal should name what is wrong, got: %v", err)
	}
}
