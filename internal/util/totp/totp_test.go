package totp

import (
	"testing"
	"time"

	"github.com/xlzd/gotp"
)

func TestVerifyWithSkewKeepsWindowBounded(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	now := time.Unix(1800000015, 0)
	for _, offset := range []int{-2, -1, 0, 1, 2} {
		code := gotp.NewDefaultTOTP(secret).AtTime(now.Add(time.Duration(offset) * 30 * time.Second))
		if got, want := VerifyWithSkew(secret, code, now), offset >= -1 && offset <= 1; got != want {
			t.Errorf("offset %d accepted=%v, want %v", offset, got, want)
		}
	}
	for _, secret := range []string{"", "invalid secret!"} {
		if VerifyWithSkew(secret, "123456", now) {
			t.Errorf("invalid secret %q accepted", secret)
		}
	}
}
