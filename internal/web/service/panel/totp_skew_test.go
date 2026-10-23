package panel

import (
	"testing"
	"time"

	"github.com/xlzd/gotp"
)

func TestLoginAcceptsAdjacentAuthenticatorWindow(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "clock-password")
	const secret = "JBSWY3DPEHPK3PXP"
	setSettings(t, map[string]string{"twoFactorEnable": "true", "twoFactorToken": secret})
	code := gotp.NewDefaultTOTP(secret).AtTime(time.Now().Add(30 * time.Second))
	user, err := (&UserService{}).CheckUser("admin", "clock-password", code)
	if err != nil || user == nil || user.Username != "admin" {
		t.Fatalf("adjacent-window login user=%v error=%v", user, err)
	}
}
