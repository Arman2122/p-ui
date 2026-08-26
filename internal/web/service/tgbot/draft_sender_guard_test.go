package tgbot

import (
	"os"
	"strings"
	"testing"
)

/*
The in-progress client draft is admin-only, and its state is keyed by chat.

userStateMgr.get(message.Chat.ID) asks "is this CHAT midway through a form",
which in a group chat is true for every member's next message. The branch that
consumed that state wrote message.Text straight into the draft — the email, the
Telegram id, the comment — with no check on who sent it, while the UsersShared
branch beside it had always called checkAdmin(message.From.ID). F293.

Asserted on the source rather than by driving telego: the handler is a closure
registered against a live bot handler group, and standing one up to prove a
one-line authorisation guard would test the framework instead of the guard. The
property is "this branch is entered only for an admin", and that is exactly what
the source says.
*/
func TestTheDraftStateBranchChecksTheSender(t *testing.T) {
	body := readRouterSource(t)

	entry := strings.Index(body, "userState, exists := userStateMgr.get(message.Chat.ID)")
	if entry < 0 {
		t.Fatal("the draft-state branch was restructured; this guard is now vacuous")
	}
	// The condition on the very next branch has to carry the sender check.
	condition := body[entry:]
	end := strings.Index(condition, "{")
	if end < 0 {
		t.Fatal("no branch follows the state lookup; this guard is now vacuous")
	}
	condition = condition[:end]
	if !strings.Contains(condition, "checkAdmin(message.From.ID)") {
		t.Fatalf("the draft-state branch is entered without checking the sender, so any member of a group chat can fill in the admin's client fields. Condition was: %q", strings.TrimSpace(condition))
	}
}

/*
And the fields that branch writes really are shared, which is why the sender
matters so much.

client_Email, client_TgID and client_Comment are package-level variables, not
per-chat state, and addClient builds the saved client from them. A write from
any chat lands in the one draft any admin is about to commit. That is a larger
structural problem than F293 and is deliberately NOT fixed here — keying the
draft per chat is new architecture — but this pins the premise, so if the
globals ever become per-chat the note above can be retired honestly.
*/
func TestTheClientDraftFieldsArePackageGlobals(t *testing.T) {
	body, err := os.ReadFile("tgbot.go")
	if err != nil {
		t.Fatalf("read tgbot.go: %v", err)
	}
	for _, name := range []string{"client_Email", "client_TgID", "client_Comment"} {
		if !strings.Contains(string(body), "\n\t"+name+" ") && !strings.Contains(string(body), "\n"+name+" ") {
			t.Skipf("%s is no longer a package-level declaration in tgbot.go; re-read the draft-sharing note in F293's commit", name)
		}
	}
}

func readRouterSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("tgbot_router.go")
	if err != nil {
		t.Fatalf("read tgbot_router.go: %v", err)
	}
	return string(body)
}
