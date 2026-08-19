package wireguard

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/wireguard/wgtest"
)

// The rig drives the adapter against wgtest rather than a mock manager, so
// Starts is a real link count and ServedUsers is read off the device.

// iface is the device InterfaceName derives for the inbound the rig builds.
const iface = "pwg7"

// fill builds a well-formed key from one repeated byte. The value is irrelevant
// — nothing here does curve arithmetic — but it must be 32 bytes and non-zero.
func fill(b byte) wgtypes.Key {
	var k wgtypes.Key
	for i := range k {
		k[i] = b
	}
	return k
}

// serverKey is the device's own private key, and ghostKey belongs to a client
// that exists only in the settings blob so its resurrection would be visible.
var (
	serverKey = fill(200).String()
	ghostKey  = fill(250).String()
)

// clients are the emails the rig mints, in order. coretest.Subject must be the
// first: the traffic, online and provisioning checks all operate on it.
var clients = []string{coretest.Subject, "b@example.com", "c@example.com"}

type rig struct {
	kernel *wgtest.Kernel
	mgr    *engine.Manager
	keys   map[string]wgtypes.Key
}

func newRig() *rig {
	kernel := wgtest.New()
	r := &rig{kernel: kernel, mgr: engine.NewManager(kernel), keys: map[string]wgtypes.Key{}}
	for i, email := range clients {
		r.keys[email] = fill(byte(i + 1))
	}
	return r
}

func (r *rig) instance(users int) core.Instance {
	inst := core.Instance{
		ID:     7,
		Kind:   Kind,
		Tag:    "inbound-7",
		Listen: "0.0.0.0",
		Port:   51820,
		Enable: true,
		Settings: fmt.Sprintf(
			`{"secretKey":%q,"address":["10.0.0.1/24"],"mtu":1420,`+
				`"clients":[{"email":"ghost@example.com","publicKey":%q}]}`,
			serverKey, ghostKey),
	}
	for i := range users {
		email := clients[i]
		// []any, not []string: this is what a settings blob decodes to.
		allowed := []any{fmt.Sprintf("10.0.0.%d/32", 11+i)}
		if i == 1 {
			// One client also routes a subnet, so the suite sees both halves: an
			// address that is an identity, and a prefix that is somebody else's too.
			allowed = append(allowed, "10.9.0.0/24")
		}
		inst.Users = append(inst.Users, core.User{
			Email:  email,
			Enable: true,
			Credentials: map[string]any{
				core.CredPublicKey:  r.keys[email].String(),
				core.CredAllowedIPs: allowed,
			},
		})
	}
	return inst
}

// starts counts devices created. A reconcile that rebuilds the link instead of
// converging it shows up here as a second create.
func (r *rig) starts() int { return r.kernel.LinkCreates }

func (r *rig) feed(email string, up, down int64) {
	r.kernel.FeedTraffic(iface, r.keys[email], up, down)
}

// restart is `ip link del` followed by `ip link add`: a new ifindex, and a
// device with no key, no addresses, no peers and no counters.
func (r *rig) restart() { r.kernel.RecreateLink(iface) }

// served reads the peer set off the device and names the client behind each key,
// which is the only way an AddUser that does nothing can be told from one that works.
func (r *rig) served() []string {
	out := make([]string, 0, len(clients))
	for _, key := range r.kernel.PeerKeys(iface) {
		for email, known := range r.keys {
			if known == key {
				out = append(out, email)
			}
		}
	}
	slices.Sort(out)
	return out
}

// hostSubjects reads the allowed-IPs off the device and names the client behind
// each key, which is the only view the adapter cannot have invented.
func (r *rig) hostSubjects() map[string][]string {
	out := map[string][]string{}
	for _, key := range r.kernel.PeerKeys(iface) {
		for email, known := range r.keys {
			if known != key {
				continue
			}
			for _, allowed := range r.kernel.AllowedIPs(iface, key) {
				out[email] = append(out[email], allowed.String())
			}
		}
	}
	return out
}

// feedSession is a handshake arriving from one outer address: the peer is on the
// wire, and the kernel keeps exactly that address until it moves.
func (r *rig) feedSession(email, source string, lastSeenUnixMilli int64) {
	addr, err := netip.ParseAddr(source)
	if err != nil {
		return
	}
	r.kernel.FeedSession(iface, r.keys[email], addr, time.UnixMilli(lastSeenUnixMilli))
}

func (r *rig) asRig() coretest.Rig {
	return coretest.Rig{
		NewCore:       func() (core.Core, error) { return NewFor(Kind, r.mgr), nil },
		Instance:      r.instance,
		Starts:        r.starts,
		FeedTraffic:   r.feed,
		RestartSource: r.restart,
		ServedUsers:   r.served,
		HostSubjects:  r.hostSubjects,
		FeedSession:   r.feedSession,
	}
}

// TestWireguardConformsToTheContract is the acceptance test for this core. A
// failure here means the contract is wrong, not that kernel WireGuard is special.
func TestWireguardConformsToTheContract(t *testing.T) {
	coretest.RunAdapterSuite(t, newRig().asRig())
}
