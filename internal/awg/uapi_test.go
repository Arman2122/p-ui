package awg

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

const vendoredHeader = "../../third_party/amneziawg-kernel/src/uapi/wireguard.h"

/*
The transcribed attribute numbers must match the vendored header, in order.

This is the failure a re-vendor invites and nothing else would catch: an
attribute inserted mid-enum shifts every number after it, so "set the junk
count" silently becomes "set the padding". The kernel accepts the message --
the type is right, the value is a plausible u16 -- and the device comes up
configured as something nobody asked for. There is no error anywhere to notice.
*/
func TestAttributeNumbersMatchTheVendoredHeader(t *testing.T) {
	got := map[string]uint16{
		"WGDEVICE_A_UNSPEC":                   devUnspec,
		"WGDEVICE_A_IFINDEX":                  devIfIndex,
		"WGDEVICE_A_IFNAME":                   devIfName,
		"WGDEVICE_A_PRIVATE_KEY":              devPrivateKey,
		"WGDEVICE_A_PUBLIC_KEY":               devPublicKey,
		"WGDEVICE_A_FLAGS":                    devFlags,
		"WGDEVICE_A_LISTEN_PORT":              devListenPort,
		"WGDEVICE_A_FWMARK":                   devFwmark,
		"WGDEVICE_A_PEERS":                    devPeers,
		"WGDEVICE_A_JC":                       devJc,
		"WGDEVICE_A_JMIN":                     devJmin,
		"WGDEVICE_A_JMAX":                     devJmax,
		"WGDEVICE_A_S1":                       devS1,
		"WGDEVICE_A_S2":                       devS2,
		"WGDEVICE_A_H1":                       devH1,
		"WGDEVICE_A_H2":                       devH2,
		"WGDEVICE_A_H3":                       devH3,
		"WGDEVICE_A_H4":                       devH4,
		"WGDEVICE_A_PEER":                     devPeer,
		"WGDEVICE_A_S3":                       devS3,
		"WGDEVICE_A_S4":                       devS4,
		"WGDEVICE_A_I1":                       devI1,
		"WGDEVICE_A_I2":                       devI2,
		"WGDEVICE_A_I3":                       devI3,
		"WGDEVICE_A_I4":                       devI4,
		"WGDEVICE_A_I5":                       devI5,
		"WGDEVICE_A_HEADER_PROTECTION_KEY":    devHeaderProtectionKey,
		"WGDEVICE_A_CONTENT_PADDING_ADDITION": devContentPaddingAddition,
		"WGDEVICE_A_REKEY_AFTER_TIME":         devRekeyAfterTime,
		"WGDEVICE_A_REKEY_TIMEOUT":            devRekeyTimeout,
		"WGDEVICE_A_REJECT_AFTER_TIME":        devRejectAfterTime,
		"WGDEVICE_A_KEEPALIVE_TIMEOUT":        devKeepaliveTimeout,
		"WGDEVICE_A_MAX_HANDSHAKE_ATTEMPTS":   devMaxHandshakeAttempts,
		"WGDEVICE_A_RANDOM_TRAILERS":          devRandomTrailers,
		"WGDEVICE_A_DISABLE_COOKIES":          devDisableCookies,
	}

	want := enumOrder(t, "wgdevice_attribute", "WGDEVICE_A_")
	if len(want) != len(got) {
		t.Fatalf("the header declares %d attributes and this package transcribes %d; re-vendoring added or removed one", len(want), len(got))
	}
	for name, number := range want {
		transcribed, ok := got[name]
		if !ok {
			t.Errorf("%s is in the header but not transcribed here", name)
			continue
		}
		if transcribed != number {
			t.Errorf("%s = %d here, but the header puts it at %d", name, transcribed, number)
		}
	}
}

// The commands are a second enum with the same hazard, and a shifted command
// number sends a GET where a SET was meant.
func TestCommandNumbersMatchTheVendoredHeader(t *testing.T) {
	want := enumOrder(t, "wg_cmd", "WG_CMD_")
	for name, number := range map[string]uint8{
		"WG_CMD_GET_DEVICE":   cmdGetDevice,
		"WG_CMD_SET_DEVICE":   cmdSetDevice,
		"WG_CMD_UNKNOWN_PEER": cmdUnknownPeer,
	} {
		if got, ok := want[name]; !ok || uint16(number) != got {
			t.Errorf("%s = %d here, header says %d (present: %v)", name, number, got, ok)
		}
	}
}

// The family name is the whole reason this package exists rather than a call to
// wgctrl, so it is read from the header rather than trusted.
func TestFamilyNameMatchesTheVendoredHeader(t *testing.T) {
	body, err := os.ReadFile(vendoredHeader)
	if err != nil {
		t.Fatalf("reading the vendored header: %v", err)
	}
	match := regexp.MustCompile(`#define WG_GENL_NAME "([^"]+)"`).FindSubmatch(body)
	if match == nil {
		t.Fatal("the vendored header declares no WG_GENL_NAME")
	}
	if string(match[1]) != FamilyName {
		t.Fatalf("FamilyName = %q, header says %q", FamilyName, match[1])
	}
}

// enumOrder reads one C enum from the vendored header and returns each member's
// position. Members are unvalued in this header, so position IS the value.
func enumOrder(t *testing.T, enumName, prefix string) map[string]uint16 {
	t.Helper()
	file, err := os.Open(vendoredHeader)
	if err != nil {
		t.Fatalf("reading the vendored header: %v", err)
	}
	defer file.Close()

	out := map[string]uint16{}
	var inside bool
	var next uint16
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "enum "+enumName):
			inside = true
			continue
		case !inside:
			continue
		case strings.HasPrefix(line, "};"):
			return out
		}
		name := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(line, "=", 2)[0]), ",")
		// __WG…_LAST and _MAX are the enum's own bookends, not attributes.
		if !strings.HasPrefix(name, prefix) || strings.HasPrefix(name, "__") {
			continue
		}
		out[name] = next
		next++
	}
	t.Fatalf("enum %s never closed in the vendored header", enumName)
	return nil
}
