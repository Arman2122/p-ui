package service

import (
	"encoding/base64"
	"strings"
	"testing"
)

/*
An SS-2022 inbound whose SERVER key is too short must be refused at the save.

The key size is fixed by the method: 16 bytes for 2022-blake3-aes-128-gcm, 32
for -aes-256-gcm and -chacha20-poly1305. Switching an existing inbound from the
128 method to a 256 one leaves the old 16-byte key in place, and xray refuses
the whole config — every inbound on the box, not just this one.

The normalizer beside this re-mints a CLIENT key of the wrong size. The server
key was never looked at.

TWO CHOICES WORTH STATING, because both directions of the obvious test pass
without the fix:

  - Refuse, do not re-mint. Rotating the server key silently invalidates every
    link already handed out, which is a worse outcome than a save that fails
    with a reason.
  - SHORTER only, not exact. The core derives an over-long key down, so a
    32-byte key on the 128 method works today and an exact-length check would
    refuse a save that is currently fine.

The direction is load-bearing: a test written 256 -> 128 passes either way and
certifies nothing. F329.
*/
func TestAShadowsocks2022ServerKeyTooShortForItsMethodIsRefused(t *testing.T) {
	key16 := base64.StdEncoding.EncodeToString(make([]byte, 16))
	key32 := base64.StdEncoding.EncodeToString(make([]byte, 32))

	cases := []struct {
		name     string
		settings string
		refused  bool
		why      string
	}{
		{
			name:     "a 16-byte key on a method that needs 32",
			settings: `{"method":"2022-blake3-aes-256-gcm","password":"` + key16 + `","clients":[]}`,
			refused:  true,
			why:      "this is the 128 -> 256 switch, and xray refuses the whole config over it",
		},
		{
			name:     "the same on chacha20, which also needs 32",
			settings: `{"method":"2022-blake3-chacha20-poly1305","password":"` + key16 + `","clients":[]}`,
			refused:  true,
			why:      "the size is a property of the method, not of the cipher family",
		},
		{
			name:     "a 32-byte key on a method that needs 16",
			settings: `{"method":"2022-blake3-aes-128-gcm","password":"` + key32 + `","clients":[]}`,
			refused:  false,
			why:      "the core derives an over-long key down, so this works today and must keep working",
		},
		{
			name:     "an exactly-sized key",
			settings: `{"method":"2022-blake3-aes-256-gcm","password":"` + key32 + `","clients":[]}`,
			refused:  false,
			why:      "the ordinary case",
		},
		{
			name:     "a legacy method takes any passphrase",
			settings: `{"method":"aes-128-gcm","password":"hunter2","clients":[]}`,
			refused:  false,
			why:      "ShadowsocksKeyBytes returns 0 and there is no size to check",
		},
		{
			name:     "a present but EMPTY server key",
			settings: `{"method":"2022-blake3-aes-256-gcm","password":"","clients":[]}`,
			refused:  true,
			why:      "it decodes to zero bytes, and sing answers ErrMissingPSK -- the same whole-config refusal. An audit found this accepted because the guard short-circuited on key == \"\"",
		},
		{
			name:     "settings with no password at all",
			settings: `{"method":"2022-blake3-aes-256-gcm","clients":[]}`,
			refused:  false,
			why:      "nothing to judge; a bare type assertion here would panic instead",
		},
		{
			name:     "a key that is not base64",
			settings: `{"method":"2022-blake3-aes-256-gcm","password":"not base64 at all!!","clients":[]}`,
			refused:  true,
			why:      "the core cannot decode it either",
		},
		{
			name:     "settings that are not JSON",
			settings: `{oh dear`,
			refused:  false,
			why:      "not this guard's business to report; the parse fails elsewhere with a better message",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := refuseShortShadowsocksServerKey(c.settings)
			if c.refused {
				if err == nil {
					t.Fatalf("accepted a key the core refuses: %s", c.why)
				}
				if !strings.Contains(err.Error(), "server key") {
					t.Fatalf("the refusal should name what is wrong, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a valid configuration (%s): %v", c.why, err)
			}
		})
	}
}
