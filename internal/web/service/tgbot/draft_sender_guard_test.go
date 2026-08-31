package tgbot

import (
	"os"
	"strings"
	"testing"
	"time"
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
And two admins no longer share one draft.

client_Email, client_TgID and the rest were package-level variables: there was
exactly one draft on the whole panel, so two admins running the wizard at the
same time wrote into it in turn, and whichever pressed Submit first saved a mix
of both -- the other's email, limits and inbound set, under their own name.

This replaces the note that used to stand here saying the sharing was deliberate
and out of F293's scope. It no longer is. F290.
*/
func TestTwoChatsDoNotShareOneClientDraft(t *testing.T) {
	first, releaseFirst := clientDraftMgr.begin(101)
	first.email = "first@admin"
	first.inboundIDs = []int{7}
	releaseFirst()

	second, releaseSecond := clientDraftMgr.begin(202)
	if second.email != "" || len(second.inboundIDs) != 0 {
		releaseSecond()
		t.Fatalf("a second chat opened the wizard onto another admin's draft: email=%q inbounds=%v", second.email, second.inboundIDs)
	}
	second.email = "second@admin"
	releaseSecond()

	back, release := clientDraftMgr.begin(101)
	defer release()
	if back.email != "first@admin" {
		t.Fatalf("the first admin's email is now %q; the second admin's wizard overwrote it", back.email)
	}
}

// An admin who starts the wizard and goes silent must not leave a draft behind.
func TestAnAbandonedDraftIsPruned(t *testing.T) {
	_, release := clientDraftMgr.begin(303)
	release()

	clientDraftMgr.mu.Lock()
	clientDraftMgr.drafts[303].at = time.Now().Add(-2 * draftIdleLife)
	clientDraftMgr.lastPrune = time.Now().Add(-2 * time.Hour)
	clientDraftMgr.mu.Unlock()

	// Any begin runs the prune; use another chat so 303 is not refreshed.
	_, releaseOther := clientDraftMgr.begin(404)
	releaseOther()

	clientDraftMgr.mu.Lock()
	_, still := clientDraftMgr.drafts[303]
	clientDraftMgr.mu.Unlock()
	if still {
		t.Fatal("a draft nobody came back to is still held; every abandoned wizard leaks one for the life of the process")
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
