package model

import (
	"encoding/base64"
	"encoding/hex"
)

// MtprotoDDSecret is the client's secret in the dd spelling a WEB client holds:
// the same sixteen key bytes the ee secret carries, with 0xdd in front and no
// domain. Empty when the stored secret is not an ee secret with a full key.
func MtprotoDDSecret(secret string) string {
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(secret)
	}
	if err != nil || len(decoded) < 1+mtprotoKeyLength || decoded[0] != 0xee {
		return ""
	}
	return "dd" + hex.EncodeToString(decoded[1:1+mtprotoKeyLength])
}

const mtprotoKeyLength = 16
