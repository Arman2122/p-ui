package worker

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func (w *Worker) journals() (*nodev1.RuntimeJournalIdentity, error) {
	source, ok := w.backend.(authority.Source)
	if !ok {
		return nil, authority.ErrReport
	}
	journal := &nodev1.RuntimeJournalIdentity{RuntimeId: w.id, CoreId: w.core, StateJournalId: w.store.JournalID(), CreditJournalId: source.AuthorityJournalID()}
	if !authority.ValidRuntime(journal) {
		return nil, authority.ErrReport
	}
	return journal, nil
}

func (w *Worker) matchesJournals(request *http.Request) bool {
	journal, err := w.journals()
	return err == nil && request.Header.Get("X-Pui-State-Journal") == journal.StateJournalId && request.Header.Get("X-Pui-Credit-Journal") == journal.CreditJournalId
}

func (w *Worker) matchesAgentJournal(request *http.Request) bool {
	id := request.Header.Get("X-Pui-Agent-Journal")
	journal, err := w.journals()
	return err == nil && id != journal.CreditJournalId && w.store.CheckAgentJournal(id) == nil
}

func (w *Worker) captureAuthority(ctx context.Context) (*nodev1.AuthorityReport, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	journal, err := w.journals()
	if err != nil {
		return nil, err
	}
	pending, err := w.store.PendingAuthority()
	if err != nil {
		return nil, err
	}
	if pending != nil {
		if pending.RuntimeId != journal.RuntimeId || pending.CreditJournalId != journal.CreditJournalId {
			return nil, state.ErrIdentity
		}
		return pending, nil
	}
	cursor, err := w.store.AuthorityCursor()
	if err != nil {
		return nil, err
	}
	page, err := w.backend.(authority.Source).Authority(ctx, cursor, authority.MaxEntries)
	if err != nil {
		return nil, err
	}
	cursors, err := w.store.Cursors()
	if err != nil {
		return nil, err
	}
	ready := w.available.Load() && !w.applying.Load() && !w.restoreFailed.Load() && cursors.Applied == cursors.Desired
	now := time.Now()
	if w.clock != nil {
		now, err = w.clock.Now()
		if err != nil {
			return nil, err
		}
	}
	return w.store.CaptureAuthority(journal, page, ready, now.UnixMilli())
}

func writeAuthority(response http.ResponseWriter, message proto.Message) {
	encoded, err := proto.Marshal(message)
	if err != nil {
		http.Error(response, "runtime authority unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Type", "application/protobuf")
	_, _ = response.Write(encoded)
}

func (w *Worker) authorityHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/journals", func(response http.ResponseWriter, request *http.Request) {
		journal, err := w.journals()
		if err != nil {
			http.Error(response, "runtime journals unavailable", http.StatusServiceUnavailable)
			return
		}
		writeAuthority(response, journal)
	})
	mux.HandleFunc("GET /v1/authority", func(response http.ResponseWriter, request *http.Request) {
		report, err := w.captureAuthority(request.Context())
		if err != nil {
			http.Error(response, "runtime authority unavailable", http.StatusServiceUnavailable)
			return
		}
		if report == nil {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		writeAuthority(response, report)
	})
	mux.HandleFunc("POST /v1/authority/ack", func(response http.ResponseWriter, request *http.Request) {
		receipt := new(nodev1.AuthorityReceipt)
		if err := readMessage(response, request, receipt, 1024); err != nil {
			http.Error(response, "invalid authority receipt", http.StatusBadRequest)
			return
		}
		if err := w.store.AcknowledgeAuthority(receipt); err != nil {
			http.Error(response, "authority receipt rejected", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
