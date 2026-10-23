package totp

import (
	"crypto/subtle"
	"encoding/base32"
	"strings"
	"time"

	"github.com/xlzd/gotp"
)

// VerifyWithSkew tolerates one authenticator clock step either side of now.
func VerifyWithSkew(secret, code string, now time.Time) bool {
	secret, code = strings.TrimSpace(secret), strings.TrimSpace(code)
	if secret == "" || len(code) != 6 {
		return false
	}
	padded := secret + strings.Repeat("=", (8-len(secret)%8)%8)
	if _, err := base32.StdEncoding.DecodeString(padded); err != nil {
		return false
	}
	otp := gotp.NewDefaultTOTP(secret)
	matched := 0
	for skew := -1; skew <= 1; skew++ {
		expected := otp.AtTime(now.Add(time.Duration(skew) * 30 * time.Second))
		matched |= subtle.ConstantTimeCompare([]byte(code), []byte(expected))
	}
	return matched == 1
}
