package tgbot

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Wizard messages require an administrator even when the chat is shared.
func TestTheDraftStateBranchChecksTheSender(t *testing.T) {
	body := readRouterSource(t)

	entry := strings.Index(body, "userState, exists := userStateMgr.get(messageActor(message))")
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

// Independent conversations must not overwrite one another's draft.
func TestTwoChatsDoNotShareOneClientDraft(t *testing.T) {
	first, releaseFirst := clientDraftMgr.begin(chatUser{chatID: 101, userID: 101})
	first.email = "first@admin"
	first.inboundIDs = []int{7}
	releaseFirst()

	second, releaseSecond := clientDraftMgr.begin(chatUser{chatID: 202, userID: 202})
	if second.email != "" || len(second.inboundIDs) != 0 {
		releaseSecond()
		t.Fatalf("a second chat opened the wizard onto another admin's draft: email=%q inbounds=%v", second.email, second.inboundIDs)
	}
	second.email = "second@admin"
	releaseSecond()

	back, release := clientDraftMgr.begin(chatUser{chatID: 101, userID: 101})
	defer release()
	if back.email != "first@admin" {
		t.Fatalf("the first admin's email is now %q; the second admin's wizard overwrote it", back.email)
	}
}

// An admin who starts the wizard and goes silent must not leave a draft behind.
func TestAnAbandonedDraftIsPruned(t *testing.T) {
	_, release := clientDraftMgr.begin(chatUser{chatID: 303, userID: 303})
	release()

	clientDraftMgr.mu.Lock()
	clientDraftMgr.drafts[chatUser{chatID: 303, userID: 303}].at = time.Now().Add(-2 * draftIdleLife)
	clientDraftMgr.lastPrune = time.Now().Add(-2 * time.Hour)
	clientDraftMgr.mu.Unlock()

	// Any begin runs the prune; use another chat so 303 is not refreshed.
	_, releaseOther := clientDraftMgr.begin(chatUser{chatID: 404, userID: 404})
	releaseOther()

	clientDraftMgr.mu.Lock()
	_, still := clientDraftMgr.drafts[chatUser{chatID: 303, userID: 303}]
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
