package service

import (
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
)

/*
pendingClientDisable is the half of a depletion that must not happen inside the
traffic transaction: rewriting inbounds.settings, and pushing the same change to
a remote node.

Both were done in-transaction and unlocked. inbounds.settings is a JSON blob
read, edited and written back whole, and every admin mutation does the same
under lockInbound — so the poll, which took no lock, raced them. An add that had
already read the blob saved its own copy afterwards and restored a depleted
client's "enable": true.

That is not transient. The add's SyncInbound flips clients.enable back as well,
and the depleted predicate only ever selects rows that are still enabled in
client_traffics — which the poll had already flipped false. Nothing re-detects
the client, both places the config builders read say enabled, and an exhausted
quota is served until someone resets that client's traffic by hand.
*/
type pendingClientDisable struct {
	local  map[int]map[string]struct{}
	remote map[int]map[string]struct{}
}

func (p pendingClientDisable) isEmpty() bool {
	return len(p.local) == 0 && len(p.remote) == 0
}

/*
applyPendingClientDisables performs the deferred writes after the traffic
transaction has committed.

The lock order is the one every admin mutation already uses: take lockInbound,
THEN the serial writer. Taking them the other way round — which is what holding
the lock inside the traffic transaction would have meant — deadlocks the panel
permanently, because an admin mutation waits for the writer while holding the
lock and the writer is what runs the traffic pass. No timeout covers it:
submitTrafficWrite's deadline applies to the enqueue, not to the work.

Re-reading the row under the lock is what makes this correct rather than merely
deadlock-free. It sees whatever an admin mutation committed in the meantime and
disables the client in that merged blob.
*/
func (s *InboundService) applyPendingClientDisables(pending pendingClientDisable) {
	if pending.isEmpty() {
		return
	}

	for inboundID, emails := range pending.local {
		s.applyOneDisable(inboundID, emails, false)
	}
	for inboundID, emails := range pending.remote {
		s.applyOneDisable(inboundID, emails, true)
	}
}

func (s *InboundService) applyOneDisable(inboundID int, emails map[string]struct{}, remote bool) {
	lock := lockInbound(inboundID)
	defer lock.Unlock()

	/*
		A client re-enabled between the commit and here is no longer ours to
		disable. The window is small but real: an operator resetting a depleted
		client's traffic in exactly that gap would otherwise be undone, and with
		its usage zeroed nothing would ever re-deplete it — the same permanent
		outcome, arrived at from the other direction.
	*/
	live := s.stillDepletedEmails(emails)
	if len(live) == 0 {
		return
	}

	err := runSerializedTx(func(tx *gorm.DB) error {
		if remote {
			return s.disableRemoteClients(tx, inboundID, live)
		}
		_, _, mErr := s.markClientsDisabledInSettings(tx, inboundID, live)
		return mErr
	})
	if err != nil {
		logger.Warning("disableInvalidClients: deferred settings sync failed for inbound", inboundID, ":", err)
	}
}

// stillDepletedEmails drops any client whose traffic row has been re-enabled
// since the depletion was decided.
func (s *InboundService) stillDepletedEmails(emails map[string]struct{}) map[string]struct{} {
	if len(emails) == 0 {
		return nil
	}
	wanted := make([]string, 0, len(emails))
	for email := range emails {
		wanted = append(wanted, email)
	}

	var reEnabled []string
	if err := database.GetDB().Model(&model.ClientRecord{}).
		Where("email IN ? AND enable = ?", wanted, true).
		Pluck("email", &reEnabled).Error; err != nil {
		// Unreadable means unknown, and disabling a client the operator may have
		// just restored is the worse mistake. Skip the pass; the next poll
		// re-decides from scratch.
		logger.Warning("disableInvalidClients: cannot confirm the depletion still stands:", err)
		return nil
	}
	if len(reEnabled) == 0 {
		return emails
	}

	live := make(map[string]struct{}, len(emails))
	for email := range emails {
		live[email] = struct{}{}
	}
	for _, email := range reEnabled {
		delete(live, email)
	}
	return live
}
