package controller

import (
	"strings"
	"testing"
)

/*
A failed session revocation must not be reported as a failed save.

UpdateAllSetting is atomic — validation first, every write inside one
transaction — and BumpLoginEpoch runs AFTER that commit, when 2FA has just been
turned on. Assigning its error to err told the operator the whole save had
failed while every setting was in fact stored.

The consequence is worse than a wrong message. They re-submit; the second submit
finds 2FA already on, so `!oldTwoFactor` is false and the bump never runs again.
The sessions enabling 2FA was meant to revoke stay valid, permanently, with
nothing anywhere saying so.

Logout, two files over, already sets the house rule for this exact call: a
failed revocation is logged loudly and the operation still reports what really
happened. F398.
*/
func TestEnabling2FAReportsAFailedRevocationWithoutFailingTheSave(t *testing.T) {
	body := readControllerSource(t, "setting.go")

	start := strings.Index(body, "func (a *SettingController) updateSetting(")
	if start < 0 {
		t.Fatal("updateSetting was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of updateSetting; this guard is now vacuous")
	}
	handler := body[start : start+end]

	if strings.Contains(handler, "err = bumpErr") {
		t.Error("a failed session revocation is still turned into a failed save, so the operator re-submits and the bump — which only runs on the transition — never happens again")
	}
	if !strings.Contains(handler, "logger.Error(") {
		t.Error("the failed revocation is not logged, which is the only place it would otherwise be recorded")
	}
	if !strings.Contains(handler, "jsonMsgObjWarn(") {
		t.Error("nothing tells the operator their other sessions are still valid")
	}
	if !strings.Contains(handler, `I18nWeb(c, "pages.settings.toasts.sessionsNotRevoked")`) {
		t.Error("the warning is not the translated one")
	}
}
