package tgbot

import (
	"sync"
	"time"
)

// A draft belongs to one administrator in one chat, never to every member of a group.
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

// Each administrator holds a separate lock for the complete wizard step.
type clientDraftStore struct {
	mu        sync.Mutex
	drafts    map[chatUser]*draftEntry
	lastPrune time.Time
}

var clientDraftMgr = &clientDraftStore{drafts: make(map[chatUser]*draftEntry)}

// draftIdleLife is how long an untouched draft survives. Longer than
// userStateMgr's hour: the state is cleared after every answered prompt, while
// the draft has to outlive the gaps between them.
const draftIdleLife = 6 * time.Hour

// Hold the draft lock across each read-modify-write step so concurrent updates cannot interleave.
func (s *clientDraftStore) begin(actor chatUser) (*clientDraft, func()) {
	s.mu.Lock()
	entry, known := s.drafts[actor]
	if !known {
		entry = &draftEntry{}
		s.drafts[actor] = entry
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
