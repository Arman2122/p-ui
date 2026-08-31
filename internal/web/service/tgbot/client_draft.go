package tgbot

import (
	"sync"
	"time"
)

/*
clientDraft is the client one admin is midway through adding.

Every field of it used to be a package-level variable, so there was exactly one
draft on the whole panel: two admins running the wizard at the same time wrote
into it in turn, and whichever pressed Submit first saved a mix of both — the
other's email, limits and inbound set, under their own name. F290.

Keyed by chat, the same key userStateMgr uses, and it has to be the same one:
the state machine decides which field the next message fills and the draft is
what it fills. Two admins sharing one GROUP chat still share a draft, because
they already share the state machine that drives it; each admin in their own
chat with the bot — which is how the wizard is used — is now isolated.
*/
type clientDraft struct {
	inboundID  int
	inboundIDs []int
	email      string
	limitIP    int
	totalGB    int64
	expiryTime int64
	enable     bool
	tgID       string
	subID      string
	comment    string
	reset      int
}

// draftEntry is one chat's draft plus the lock a handler holds for as long as
// it is working on it, so the read-modify-writes inside stay atomic.
type draftEntry struct {
	mu    sync.Mutex
	at    time.Time
	draft clientDraft
}

/*
clientDraftStore holds one draft per chat.

Telego runs every update in its own goroutine, so two updates from one chat
overlap — which is why a handler takes that chat's lock rather than a snapshot.
The lock is per chat, so one admin's wizard never waits on another's.
*/
type clientDraftStore struct {
	mu        sync.Mutex
	drafts    map[int64]*draftEntry
	lastPrune time.Time
}

var clientDraftMgr = &clientDraftStore{drafts: make(map[int64]*draftEntry)}

// draftIdleLife is how long an untouched draft survives. Longer than
// userStateMgr's hour: the state is cleared after every answered prompt, while
// the draft has to outlive the gaps between them.
const draftIdleLife = 6 * time.Hour

/*
begin returns this chat's draft with its lock held, and the release to defer.

Held for the whole handler on purpose: a caller that reads a field, decides on
it and writes it back is the normal shape here, and a snapshot would let the
concurrent update win the decision and lose the write.
*/
func (s *clientDraftStore) begin(chatID int64) (*clientDraft, func()) {
	s.mu.Lock()
	entry, known := s.drafts[chatID]
	if !known {
		entry = &draftEntry{}
		s.drafts[chatID] = entry
	}
	// Refreshed before the prune, so an entry in use is never a stale one.
	entry.at = time.Now()
	s.pruneLocked()
	s.mu.Unlock()

	entry.mu.Lock()
	return &entry.draft, entry.mu.Unlock
}

// pruneLocked drops drafts nobody came back to, so an admin who starts the
// wizard and goes silent does not leave one behind forever.
func (s *clientDraftStore) pruneLocked() {
	now := time.Now()
	if now.Sub(s.lastPrune) < time.Hour {
		return
	}
	s.lastPrune = now
	for chatID, entry := range s.drafts {
		if now.Sub(entry.at) > draftIdleLife {
			delete(s.drafts, chatID)
		}
	}
}
