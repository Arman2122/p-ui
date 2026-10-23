package worker

import (
	"context"
	"net/http"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type CreditBackend interface {
	ApplyCredit(context.Context, []authority.Credit) error
}

func (w *Worker) ApplyCredit(ctx context.Context, batch *nodev1.CreditUpdateBatch) error {
	if err := authority.ValidateCreditBatch(batch, false); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	journal, err := w.journals()
	if err != nil {
		return err
	}
	id := w.store.Identity()
	if batch.NodeId != id.NodeID || batch.InstallationId != id.InstallationID || batch.RuntimeId != w.id || batch.StateJournalId != journal.StateJournalId || batch.CreditJournalId != journal.CreditJournalId {
		return state.ErrIdentity
	}
	if err := w.store.RequireAgentJournal(batch.AgentStateJournalId); err != nil {
		return err
	}
	cursors, err := w.store.Cursors()
	if err != nil {
		return err
	}
	if !w.available.Load() || w.restoreFailed.Load() || w.applying.Load() || cursors.Desired != cursors.Applied || cursors.Applied != batch.BaseConfigurationRevision {
		return state.ErrRevision
	}
	backend, ok := w.backend.(CreditBackend)
	if !ok {
		return authority.ErrReport
	}
	return backend.ApplyCredit(ctx, authority.Credits(batch))
}

func (w *Worker) creditRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/credit", func(response http.ResponseWriter, request *http.Request) {
		batch := new(nodev1.CreditUpdateBatch)
		if err := readMessage(response, request, batch, 256<<10); err != nil {
			http.Error(response, "invalid credit update", http.StatusBadRequest)
			return
		}
		if err := w.ApplyCredit(request.Context(), batch); err != nil {
			http.Error(response, "credit update rejected", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
