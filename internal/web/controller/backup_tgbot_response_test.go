package controller

import (
	"os"
	"strings"
	"testing"
)

/*
POST /panel/api/backuptotgbot answered with nothing at all.

The handler called SendBackupToAdmins and returned, so Gin emitted 200 with zero
bytes — while the published spec promises the panel's standard
{success, msg, obj} envelope. Any client doing resp.json() on a documented
endpoint got a parse error instead.

The empty body also hid the outcome, which is the part that matters: the send
is a silent no-op when the bot is not running, and a dump that fails to be taken
is logged and nothing more. A backup that never left the machine and one
delivered to every admin were the same 200.

Driven at the source, because the handler needs a session, a database and a live
bot — none of which decides whether a response is written. F279.
*/
func TestBackupToTgbotWritesAResponse(t *testing.T) {
	body := readControllerSource(t, "api.go")

	start := strings.Index(body, "func (a *APIController) BackuptoTgbot(")
	if start < 0 {
		t.Fatal("BackuptoTgbot was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of BackuptoTgbot; this guard is now vacuous")
	}
	handler := body[start : start+end]

	if !strings.Contains(handler, "jsonMsg(c,") {
		t.Fatal("the handler writes no body, so Gin emits 200 with zero bytes against a spec that documents a JSON envelope")
	}
	if !strings.Contains(handler, "if err := a.Tgbot.SendBackupToAdmins(); err != nil {") {
		t.Fatal("the handler ignores the send's outcome, so a backup that never left the machine reports the same 200 as one delivered to every admin")
	}
}

// And the service really has an outcome to report: it used to return nothing,
// no-opping when the bot was down and swallowing a failed dump.
func TestSendBackupToAdminsReportsWhatHappened(t *testing.T) {
	source, err := os.ReadFile("../service/tgbot/tgbot_report.go")
	if err != nil {
		t.Fatalf("read tgbot_report.go: %v", err)
	}
	if !strings.Contains(string(source), "func (t *Tgbot) SendBackupToAdmins() error {") {
		t.Fatal("SendBackupToAdmins returns nothing, so no caller can tell a delivered backup from a bot that is not running")
	}
}
