package xray

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/util/random"
)

/*
Shadowsocks client-key knowledge, in the engine so the core adapter and the
panel's settings-healing pass read one implementation. SS2022 keys are raw AEAD
keys with an exact byte size per method; legacy methods take any passphrase.
*/

// ShadowsocksMethodFromSettings reads the inbound-level method, or "" when the
// settings do not carry one.
func ShadowsocksMethodFromSettings(settings string) string {
	if settings == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(settings), &m); err != nil {
		return ""
	}
	method, _ := m["method"].(string)
	return method
}

// RandomShadowsocksClientKey mints a key the method will accept.
func RandomShadowsocksClientKey(method string) string {
	if n := ShadowsocksKeyBytes(method); n > 0 {
		return random.Base64Bytes(n)
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// ValidShadowsocksClientKey reports whether the method can serve this key.
func ValidShadowsocksClientKey(method, key string) bool {
	n := ShadowsocksKeyBytes(method)
	if n == 0 {
		return key != ""
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return false
	}
	return len(decoded) == n
}

// ShadowsocksKeyBytes is the exact raw key size an SS2022 method demands, or 0
// for a legacy method that takes any passphrase.
func ShadowsocksKeyBytes(method string) int {
	switch method {
	case "2022-blake3-aes-128-gcm":
		return 16
	case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		return 32
	}
	return 0
}
