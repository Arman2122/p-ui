package service

import (
	"testing"
	"time"

	"github.com/xlzd/gotp"
)

func TestSettingsAcceptAdjacentAuthenticatorWindow(t *testing.T) {
	setupSettingTestDB(t)
	const secret = "JBSWY3DPEHPK3PXP"
	svc := &SettingService{}
	if err := svc.SetTwoFactorToken(secret); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTwoFactorEnable(true); err != nil {
		t.Fatal(err)
	}
	code := gotp.NewDefaultTOTP(secret).AtTime(time.Now().Add(30 * time.Second))
	if err := svc.VerifyTwoFactorCode(code); err != nil {
		t.Fatalf("adjacent-window settings confirmation: %v", err)
	}
}
