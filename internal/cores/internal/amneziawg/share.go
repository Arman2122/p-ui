package amneziawg

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
)

/*
RenderClient is the WireGuard .conf plus the obfuscation, and the obfuscation is
not optional decoration.

Every parameter has to be identical on both ends: a client whose junk count or
message headers differ does not connect slightly worse, it cannot recognise the
server's packets at all. So a .conf handed out without them describes a tunnel
that completes nothing, and the operator's symptom is "AmneziaWG just doesn't
work" with no error anywhere.
*/
func (c *Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	share, err := c.Core.RenderClient(inst, user, host)
	if err != nil {
		return core.Share{}, err
	}
	params, err := ParamsOf(inst)
	if err != nil {
		return core.Share{}, err
	}
	if params.IsZero() {
		return share, nil
	}
	share.Body = insertParams(share.Body, params)
	return share, nil
}

// ParamsOf reads an inbound's obfuscation out of its settings.
func ParamsOf(inst core.Instance) (awg.Params, error) {
	if inst.Settings == "" {
		return awg.Params{}, nil
	}
	var settings struct {
		AWG awg.Params `json:"awg"`
	}
	if err := json.Unmarshal([]byte(inst.Settings), &settings); err != nil {
		return awg.Params{}, fmt.Errorf("awgkernel: inbound %d settings: %w", inst.ID, err)
	}
	return settings.AWG, nil
}

/*
insertParams puts the parameters at the end of [Interface], which is where every
Amnezia client reads them.

Spliced at the blank line that ends the section rather than appended, because a
key after [Peer] belongs to the peer: the client would either reject the file or
quietly ignore the parameter, and ignoring it is the failure that looks like
nothing at all.
*/
func insertParams(conf string, p awg.Params) string {
	var b strings.Builder
	writeRange16 := func(key string, value uint16) {
		if value != 0 {
			fmt.Fprintf(&b, "%s = %d\n", key, value)
		}
	}
	writeHeader := func(key string, value uint64) {
		if value != 0 {
			fmt.Fprintf(&b, "%s = %s\n", key, rangeString(uint32(value), uint32(value>>32)))
		}
	}
	writeTimer := func(key string, value uint32) {
		if value != 0 {
			fmt.Fprintf(&b, "%s = %s\n", key, rangeString(uint32(uint16(value)), uint32(uint16(value>>16))))
		}
	}
	writeText := func(key, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s = %s\n", key, value)
		}
	}

	writeRange16("Jc", p.Jc)
	writeRange16("Jmin", p.Jmin)
	writeRange16("Jmax", p.Jmax)
	writeRange16("S1", p.S1)
	writeRange16("S2", p.S2)
	writeRange16("S3", p.S3)
	writeRange16("S4", p.S4)
	writeHeader("H1", p.H1)
	writeHeader("H2", p.H2)
	writeHeader("H3", p.H3)
	writeHeader("H4", p.H4)
	writeText("I1", p.I1)
	writeText("I2", p.I2)
	writeText("I3", p.I3)
	writeText("I4", p.I4)
	writeText("I5", p.I5)
	writeText("HeaderProtectionKey", p.HeaderProtectionKey)
	writeTimer("ContentPaddingAddition", p.ContentPaddingAddition)
	writeTimer("RekeyAfterTime", p.RekeyAfterTime)
	writeTimer("RekeyTimeout", p.RekeyTimeout)
	writeTimer("RejectAfterTime", p.RejectAfterTime)
	writeTimer("KeepaliveTimeout", p.KeepaliveTimeout)
	writeTimer("MaxHandshakeAttempts", p.MaxHandshakeAttempts)
	if p.RandomTrailers {
		b.WriteString("RandomTrailers = on\n")
	}
	if p.DisableCookies {
		b.WriteString("DisableCookies = on\n")
	}

	block := b.String()
	if block == "" {
		return conf
	}
	// The blank line closing [Interface]. Without one the file has no [Peer] to
	// separate from, so appending is the same thing.
	if at := strings.Index(conf, "\n\n"); at >= 0 {
		return conf[:at+1] + block + conf[at+1:]
	}
	return conf + block
}

// rangeString writes a range the way awg-tools reads one: "lo-hi", or a bare
// number when the bounds are equal, which the parser expands back to [n, n].
func rangeString(lo, hi uint32) string {
	if lo == hi {
		return fmt.Sprintf("%d", lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}
