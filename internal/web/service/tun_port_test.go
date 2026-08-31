package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An Xray TUN device binds no socket, and the panel used to say it did.

Transports() had no "tun" arm, so it fell to `default: out.TCP = true` and
checkPortConflict counted the tun inbound's stored port as occupied -- naming a
conflict with something that is not listening, and refusing a real inbound on a
free port.

The one-line arm does not work: inboundTransports turns an empty answer back
into TCP on purpose, so a core cannot disable the check for its own kind by
being careless. The fix is a field that tells the two apart. F319.
*/
func TestATunInboundDoesNotHoldItsPort(t *testing.T) {
	setupConflictDB(t)

	seedInboundConflict(t, "tun-dev", "", 47101, "tun", "", `{}`)

	real := &model.Inbound{
		Tag: "vless-real", Enable: true, Listen: "", Port: 47101,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
	}
	hit, err := (&InboundService{}).checkPortConflict(real, 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit != nil {
		t.Fatalf("a real inbound was refused port %d because a TUN device holds it; a TUN device is a kernel interface and binds no socket (conflict named %q)", real.Port, hit.Tag)
	}
}

// And the fail-safe still stands: a kind whose core answers an EMPTY Transports
// keeps holding its port, because that is negligence, not a declaration.
func TestAKindThatDeclaresNothingStillHoldsItsPort(t *testing.T) {
	setupConflictDB(t)

	seedInboundConflict(t, "vless-first", "", 47102, model.VLESS, "",
		`{"clients":[],"decryption":"none"}`)

	second := &model.Inbound{
		Tag: "vless-second", Enable: true, Listen: "", Port: 47102,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
	}
	hit, err := (&InboundService{}).checkPortConflict(second, 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit == nil {
		t.Fatal("two TCP inbounds on one port were both accepted; the process would fail to start")
	}
}

// A tun inbound saves with no port at all, which is the honest value for a
// device that binds none. Every other kind still has to name one.
func TestATunInboundSavesWithoutAPort(t *testing.T) {
	if err := validateInboundBind(&model.Inbound{Protocol: "tun", Port: 0}); err != nil {
		t.Fatalf("a TUN inbound was refused for having no port, which is what it binds: %v", err)
	}
	err := validateInboundBind(&model.Inbound{Protocol: model.VLESS, Port: 0})
	if err == nil {
		t.Fatal("a VLESS inbound with no port was accepted; the core binds whatever ephemeral port the OS hands it")
	}
	if !strings.Contains(err.Error(), "port must be between 1 and 65535") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}
