package wireguard

import "testing"

/*
An AmneziaWG device needs a smaller MTU than the module gives it.

src/device.c:324 sets `dev->mtu = ETH_DATA_LEN - overhead` when the device is
created, where overhead is plain WireGuard's: MESSAGE_MINIMUM_LENGTH + a UDP
header + the larger of an IPv6 and an IPv4 header, 32+8+40 = 80, so 1420. It is
never revised.

S4 is then prepended to every data packet by skb_push (src/send.c:320) with
nothing clamping it against the path. Its two neighbours look like they would do
the same and do not, which is why only S4 appears here: ContentPaddingAddition
is clamped to the device MTU by randomize_skb_padding, and RandomTrailers is
bounded by the largest packet already sent.

The arithmetic matters because Params.Validate FORCES S4 >= 12 whenever a
header-protection key is set. At S4=12 over an IPv6 outer header the wire packet
is 1420+16+16+12+8+40 = 1512 against a 1500 path, so every header-protected
device was over the limit by construction. The symptom is the one nobody
diagnoses: it connects, small packets are fine, and large transfers stall.
F103.
*/
func TestTheDeviceMTUAccountsForTransportPadding(t *testing.T) {
	cases := []struct {
		name       string
		configured int
		padding    int
		want       int
		why        string
	}{
		{
			name:       "plain WireGuard keeps the module's own default",
			configured: 0,
			padding:    0,
			want:       DefaultMTU,
			why:        "no obfuscation, nothing to subtract",
		},
		{
			name:       "the smallest S4 header protection allows",
			configured: 0,
			padding:    12,
			want:       1408,
			why:        "12 is HeaderProtectionNonceSize, the minimum Validate enforces",
		},
		{
			name:       "a large S4",
			configured: 0,
			padding:    50,
			want:       1370,
			why:        "every byte of S4 comes off the payload budget",
		},
		{
			name:       "an operator's explicit MTU wins",
			configured: 1280,
			padding:    50,
			want:       1280,
			why:        "a stored value is the operator's decision, not ours to re-derive",
		},
		{
			name:       "an explicit MTU wins even with no obfuscation",
			configured: 1500,
			padding:    0,
			want:       1500,
			why:        "same reason",
		},
		{
			name:       "an absurd S4 floors at IPv6's minimum",
			configured: 0,
			padding:    900,
			want:       MinMTU,
			why:        "below 1280 the tunnel cannot carry IPv6 at all; that S4 is a validation problem",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeviceMTU(c.configured, c.padding); got != c.want {
				t.Fatalf("DeviceMTU(%d, %d) = %d, want %d — %s", c.configured, c.padding, got, c.want, c.why)
			}
		})
	}
}

// And the value actually reaches the link the kernel is asked for, which is the
// half a pure-arithmetic test cannot see.
func TestTheDerivedMTUReachesTheLinkSpec(t *testing.T) {
	if got := linkMTU(0, 50); got != 1370 {
		t.Fatalf("linkMTU(0, 50) = %d, want 1370: the device is created at the module's default and the padding is never subtracted", got)
	}
	if got := linkMTU(1400, 50); got != 1400 {
		t.Fatalf("linkMTU(1400, 50) = %d, want the operator's own 1400", got)
	}
}
