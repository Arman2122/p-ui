package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"
)

// Runtime effects wait for commit; settings writes also wait for lockInbound,
// which must be acquired outside the traffic writer to avoid lock inversion.
type pendingTrafficApply struct {
	local    map[int]map[string]struct{}
	remote   map[int]map[string]struct{}
	renewals []renewedClient
	inbounds []*model.Inbound
}

func (p pendingTrafficApply) hasDisables() bool {
	return len(p.local) > 0 || len(p.remote) > 0 || len(p.inbounds) > 0
}

// emails lists every client the pass switched off, on any inbound.
func (p pendingTrafficApply) emails() []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, byInbound := range []map[int]map[string]struct{}{p.local, p.remote} {
		for _, byEmail := range byInbound {
			for email := range byEmail {
				if _, dup := seen[email]; dup {
					continue
				}
				seen[email] = struct{}{}
				out = append(out, email)
			}
		}
	}
	return out
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
func (s *InboundService) applyPendingClientDisables(pending pendingTrafficApply) (needRestart bool) {
	if !pending.hasDisables() {
		return
	}

	for inboundID, emails := range pending.local {
		needRestart = s.applyOneDisable(inboundID, emails, false) || needRestart
	}
	for inboundID, emails := range pending.remote {
		needRestart = s.applyOneDisable(inboundID, emails, true) || needRestart
	}
	for _, inbound := range pending.inbounds {
		needRestart = s.applyInboundDisable(inbound.Id) || needRestart
	}
	return
}

func (s *InboundService) applyOneDisable(inboundID int, emails map[string]struct{}, remote bool) (needRestart bool) {
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

	// The push is staged here and made below, once the writer has released:
	// runSerializedTx asks for network I/O to stay out of it, and a node that
	// has gone away would otherwise hold the one traffic writer for its whole
	// remote timeout. F392.
	var oldSnapshot, updated *model.Inbound
	err := runSerializedTx(func(tx *gorm.DB) error {
		before, after, mErr := s.markClientsDisabledInSettings(tx, inboundID, live)
		if mErr != nil {
			return mErr
		}
		oldSnapshot, updated = before, after
		return nil
	})
	if err != nil {
		logger.Warning("disableInvalidClients: deferred settings sync failed for inbound", inboundID, ":", err)
		return
	}
	if updated == nil {
		return
	}
	if remote {
		if pushErr := s.pushDisabledClientsToNode(oldSnapshot, updated); pushErr != nil {
			logger.Warning("disableInvalidClients: node push failed for inbound", inboundID, ":", pushErr)
		}
		return
	}
	rt, err := s.runtimeFor(updated)
	if err != nil {
		logger.Warning("disableInvalidClients: runtime lookup after commit failed:", err)
		return true
	}
	for email := range live {
		if err := rt.RemoveUser(context.Background(), updated, email); err != nil && !restartCannotFix(err, email) {
			logger.Warning("disableInvalidClients: runtime removal after commit failed:", err)
			needRestart = true
		}
	}
	return
}

func (s *InboundService) applyInboundDisable(id int) bool {
	defer lockInbound(id).Unlock()
	inbound, err := s.GetInbound(id)
	if err != nil || inbound.Enable {
		return false
	}
	rt, err := s.runtimeFor(inbound)
	if err == nil {
		err = rt.DelInbound(context.Background(), inbound)
	}
	if err != nil && !xray.IsMissingHandlerErr(err) {
		logger.Warning("disableInvalidInbounds: runtime drop after commit failed:", err)
		return true
	}
	return false
}

func (s *InboundService) applyRenewedClients(clients []renewedClient) (needRestart bool) {
	for _, client := range clients {
		if err := s.applyRenewedClient(client); err != nil {
			logger.Warning("autoRenewClients: runtime restore after commit failed:", err)
			needRestart = true
		}
	}
	return
}

func (s *InboundService) applyRenewedClient(client renewedClient) error {
	defer lockInbound(client.inbound.Id).Unlock()
	inbound, err := s.GetInbound(client.inbound.Id)
	if err != nil || !inbound.Enable {
		return err
	}
	traffic, err := s.GetClientTrafficByEmail(client.email)
	if err != nil || traffic == nil || !traffic.Enable {
		return err
	}
	record, err := s.clientService.GetRecordByEmail(nil, client.email)
	if err != nil || !record.Enable {
		return err
	}
	rt, err := s.runtimeFor(inbound)
	if err != nil {
		return err
	}
	return rt.AddUser(context.Background(), inbound, client.email)
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
