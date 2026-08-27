package link

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

/*
NormalizePinHex renders a SHA-256 certificate pin in the one form xray-core
reads, and returns the input untouched when it is not a pin at all.

The core parses pinnedPeerCertSha256 as comma-separated hex, 32 bytes each,
colons permitted (infra/conf/transport_security.go:364-378). Most tooling prints
a pin in base64, so pasting one is the natural mistake, and the config is then
refused with the core's own message.

This is applied where a pin CROSSES a boundary that has a required format --
into a share link, into a generated outbound -- and never to what the operator
typed and the panel stored. A value that is neither hex nor base64 passes
through so the core reports it, rather than the panel silently swallowing a
typo. Ledger L21.
*/
func NormalizePinHex(pin string) string {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return pin
	}
	if h := strings.ReplaceAll(pin, ":", ""); len(h) == hex.EncodedLen(sha256.Size) {
		if _, err := hex.DecodeString(h); err == nil {
			return strings.ToLower(h)
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(pin); err == nil && len(b) == sha256.Size {
			return hex.EncodeToString(b)
		}
	}
	return pin
}

// NormalizePinList applies NormalizePinHex to a comma-separated list, which is
// the shape both the pcs link param and the core's own field carry.
func NormalizePinList(pins string) string {
	if pins == "" {
		return pins
	}
	parts := strings.Split(pins, ",")
	for i, p := range parts {
		parts[i] = NormalizePinHex(p)
	}
	return strings.Join(parts, ",")
}
