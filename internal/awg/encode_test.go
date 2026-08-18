package awg

import (
	"testing"

	"github.com/mdlayher/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func decodeAttrs(t *testing.T, attrs []netlink.Attribute) map[uint16]netlink.Attribute {
	t.Helper()
	out := make(map[uint16]netlink.Attribute, len(attrs))
	for _, attr := range attrs {
		out[attr.Type&^netlink.Nested] = attr
	}
	return out
}

/*
An unset parameter must emit NOTHING.

The kernel reads an absent attribute as "leave it alone", so emitting every
field instead would rewrite parameters the operator never touched on every
reconcile -- and on a live device rewriting the obfuscation is a rekey for every
client connected to it. A device with no obfuscation configured has to reach the
kernel as plain WireGuard, not as a device with two dozen zeroes.
*/
func TestUnsetParametersEmitNothing(t *testing.T) {
	attrs, err := encodeConfig("awg0", Config{})
	if err != nil {
		t.Fatalf("encodeConfig: %v", err)
	}
	byType := decodeAttrs(t, attrs)

	if _, ok := byType[devIfName]; !ok {
		t.Fatal("the device name is the one attribute always required")
	}
	for name, kind := range map[string]uint16{
		"jc": devJc, "s1": devS1, "h1": devH1, "i1": devI1,
		"randomTrailers": devRandomTrailers, "disableCookies": devDisableCookies,
		"listenPort": devListenPort, "privateKey": devPrivateKey,
	} {
		if _, ok := byType[kind]; ok {
			t.Errorf("%s was emitted for an empty config; the kernel would take it as an instruction", name)
		}
	}
}

// Every parameter that IS set must reach the wire, since a dropped one is a
// tunnel whose far end cannot recognise its packets.
func TestSetParametersReachTheWire(t *testing.T) {
	port := 51820
	attrs, err := encodeConfig("awg0", Config{
		ListenPort: &port,
		Params: Params{
			Jc: 4, Jmin: 40, Jmax: 70,
			S1: 20, S2: 30, S3: 40, S4: 50,
			H1: HeaderRange(10, 19), H2: HeaderRange(20, 29),
			H3: HeaderRange(30, 39), H4: HeaderRange(40, 49),
			I1: "b0xdeadbeef", HeaderProtectionKey: "secret",
			ContentPaddingAddition: 16, RandomTrailers: true, DisableCookies: true,
			RekeyAfterTime: 120, MaxHandshakeAttempts: 5,
		},
	})
	if err != nil {
		t.Fatalf("encodeConfig: %v", err)
	}
	byType := decodeAttrs(t, attrs)

	for name, kind := range map[string]uint16{
		"jc": devJc, "jmin": devJmin, "jmax": devJmax,
		"s1": devS1, "s2": devS2, "s3": devS3, "s4": devS4,
		"h1": devH1, "h2": devH2, "h3": devH3, "h4": devH4,
		"i1": devI1, "headerProtectionKey": devHeaderProtectionKey,
		"contentPaddingAddition": devContentPaddingAddition,
		"randomTrailers":         devRandomTrailers, "disableCookies": devDisableCookies,
		"rekeyAfterTime": devRekeyAfterTime, "maxHandshakeAttempts": devMaxHandshakeAttempts,
		"listenPort": devListenPort,
	} {
		if _, ok := byType[kind]; !ok {
			t.Errorf("%s was set but never reached the wire", name)
		}
	}

	// A NUL_STRING without its terminator is rejected by the kernel's policy
	// rather than truncated, so the tunnel simply never comes up.
	if data := byType[devI1].Data; data[len(data)-1] != 0 {
		t.Error("i1 is an NLA_NUL_STRING and must carry its terminator")
	}
	if got := len(byType[devH1].Data); got != 8 {
		t.Errorf("h1 is a u64 range and must be 8 bytes, got %d", got)
	}
}

// An invalid combination must be refused before it is encoded, or the operator's
// only symptom is EINVAL from a device that will not configure.
func TestEncodeRefusesAnInvalidConfig(t *testing.T) {
	if _, err := encodeConfig("awg0", Config{Params: Params{Jmin: 70, Jmax: 40}}); err == nil {
		t.Fatal("an inverted junk range must be refused before it reaches the kernel")
	}
}

/*
A peer's advanced-security value is meaningless without its flag.

The kernel ignores WGPEER_A_ADVANCED_SECURITY unless WGPEER_F_HAS_ADVANCED_SECURITY
is set, so without the flag "off" and "unset" are the same message -- and a peer
switched on could never be switched back.
*/
func TestAdvancedSecurityCarriesItsFlag(t *testing.T) {
	for _, on := range []bool{true, false} {
		raw, err := encodePeer(Peer{PublicKey: wgtypes.Key{}, AdvancedSecurity: &on})
		if err != nil {
			t.Fatalf("encodePeer: %v", err)
		}
		decoder, err := netlink.NewAttributeDecoder(raw)
		if err != nil {
			t.Fatalf("decoding the peer: %v", err)
		}
		var flags uint32
		var sawValue bool
		for decoder.Next() {
			switch decoder.Type() {
			case peerFlags:
				flags = decoder.Uint32()
			case peerAdvancedSecurity:
				sawValue = true
			}
		}
		if !sawValue {
			t.Fatalf("advancedSecurity=%v emitted no value", on)
		}
		if flags&peerHasAdvancedSec == 0 {
			t.Fatalf("advancedSecurity=%v emitted no HAS_ADVANCED_SECURITY flag, so the kernel ignores it", on)
		}
	}
}

/*
A peer's allowed IPs decide which packets reach it at all.

Left out, the peer completes a handshake and then silently drops everything --
which an operator reads as a working tunnel that carries no traffic, with no
error anywhere to explain it.
*/
func TestAllowedIPsReachThePeer(t *testing.T) {
	raw, err := encodePeer(Peer{
		PublicKey:  wgtypes.Key{},
		AllowedIPs: []string{"10.8.0.4/32", "fd00::4/128"},
	})
	if err != nil {
		t.Fatalf("encodePeer: %v", err)
	}
	decoder, err := netlink.NewAttributeDecoder(raw)
	if err != nil {
		t.Fatalf("decoding the peer: %v", err)
	}

	var families []uint16
	for decoder.Next() {
		if decoder.Type() != peerAllowedIPs {
			continue
		}
		inner, err := netlink.NewAttributeDecoder(decoder.Bytes())
		if err != nil {
			t.Fatalf("decoding allowed ips: %v", err)
		}
		for inner.Next() {
			one, err := netlink.NewAttributeDecoder(inner.Bytes())
			if err != nil {
				t.Fatalf("decoding one allowed ip: %v", err)
			}
			for one.Next() {
				if one.Type() == allowedIPFamily {
					families = append(families, one.Uint16())
				}
			}
		}
	}
	if len(families) != 2 {
		t.Fatalf("got %d allowed ips, want 2", len(families))
	}
	if families[0] != afInet || families[1] != afInet6 {
		t.Fatalf("families = %v, want [%d %d]", families, afInet, afInet6)
	}
}

// A prefix with host bits set must be masked, or the route the kernel stores is
// not the route the operator wrote.
func TestAllowedIPsAreMasked(t *testing.T) {
	if _, err := encodeAllowedIPs([]string{"10.8.0.4/24"}); err != nil {
		t.Fatalf("a prefix with host bits is normal input, not an error: %v", err)
	}
	if _, err := encodeAllowedIPs([]string{"not-a-prefix"}); err == nil {
		t.Fatal("an unparseable prefix must be refused rather than skipped")
	}
}
