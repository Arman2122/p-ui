package sub

import (
	"encoding/json"
	"strconv"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The obfuscation, carried in the share link.

A wireguard:// link for an AmneziaWG inbound used to describe a plain WireGuard
tunnel: same scheme, no parameters. The subscription page builds its downloadable
config FROM that link, so what an operator downloaded could not connect to their
own server -- the handshake never completes when one side obfuscates and the
other does not, silently, on both ends.

Named as the .conf names them, lower-cased like every other key in these links,
so the config derived from a link and the config served as a file are the same
file.
*/
func awgLinkParams(inbound *model.Inbound, params map[string]string) {
	if inbound == nil || inbound.Settings == "" {
		return
	}
	var settings struct {
		AWG awg.Params `json:"awg"`
	}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return
	}
	p := settings.AWG
	if p.IsZero() {
		return
	}

	num := func(key string, value uint16) {
		if value > 0 {
			params[key] = strconv.Itoa(int(value))
		}
	}
	text := func(key, value string) {
		if value != "" {
			params[key] = value
		}
	}

	num("jc", p.Jc)
	num("jmin", p.Jmin)
	num("jmax", p.Jmax)
	num("s1", p.S1)
	num("s2", p.S2)
	num("s3", p.S3)
	num("s4", p.S4)
	text("h1", awg.FormatHeaderRange(p.H1))
	text("h2", awg.FormatHeaderRange(p.H2))
	text("h3", awg.FormatHeaderRange(p.H3))
	text("h4", awg.FormatHeaderRange(p.H4))
	text("i1", p.I1)
	text("i2", p.I2)
	text("i3", p.I3)
	text("i4", p.I4)
	text("i5", p.I5)
	text("headerprotectionkey", p.HeaderProtectionKey)
	text("contentpaddingaddition", awg.FormatTimerRange(p.ContentPaddingAddition))
	text("rekeyaftertime", awg.FormatTimerRange(p.RekeyAfterTime))
	text("rekeytimeout", awg.FormatTimerRange(p.RekeyTimeout))
	text("rejectaftertime", awg.FormatTimerRange(p.RejectAfterTime))
	text("keepalivetimeout", awg.FormatTimerRange(p.KeepaliveTimeout))
	text("maxhandshakeattempts", awg.FormatTimerRange(p.MaxHandshakeAttempts))
	if p.RandomTrailers {
		params["randomtrailers"] = "on"
	}
	if p.DisableCookies {
		params["disablecookies"] = "on"
	}
}
