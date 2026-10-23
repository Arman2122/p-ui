package agent

import (
	"bytes"
	"context"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func (l *LocalRuntimes) receiveCredit(ctx context.Context, client nodev1.CreditServiceClient) error {
	id := l.store.Identity()
	return l.runtimePass(ctx, func(ctx context.Context, worker localRuntime) error {
		journal, err := l.bindWorkerJournal(ctx, worker)
		if err != nil {
			return err
		}
		request := &nodev1.CreditDeliveryRequest{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: l.store.JournalID(), RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId}
		batch, err := client.Fetch(ctx, request)
		if err != nil {
			return err
		}
		if authority.ValidateCreditBatch(batch, true) != nil || batch.NodeId != request.NodeId || batch.InstallationId != request.InstallationId || batch.AgentStateJournalId != request.AgentStateJournalId || batch.RuntimeId != request.RuntimeId || batch.StateJournalId != request.StateJournalId || batch.CreditJournalId != request.CreditJournalId {
			return state.ErrIdentity
		}
		if len(batch.Updates) == 0 {
			return nil
		}
		cursors, err := l.store.Cursors()
		if err != nil {
			return err
		}
		if cursors.Applied != cursors.Desired || cursors.Applied != batch.BaseConfigurationRevision {
			return state.ErrRevision
		}
		if err := worker.post(ctx, "/v1/credit", batch); err != nil {
			return err
		}
		digest, err := authority.CreditDigest(batch)
		if err != nil {
			return err
		}
		receipt, err := client.Acknowledge(ctx, batch)
		if err != nil {
			return err
		}
		if receipt == nil || !bytes.Equal(receipt.PayloadSha256, digest) {
			return authority.ErrReport
		}
		return nil
	})
}

func (a *Agent) Credit(ctx context.Context) error {
	local, ok := a.applier.(*LocalRuntimes)
	if !ok {
		return authority.ErrReport
	}
	conn, err := a.connection()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := nodev1.NewCreditServiceClient(conn)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		err := local.receiveCredit(ctx, client)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
