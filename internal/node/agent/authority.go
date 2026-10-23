package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type JournalProvider interface {
	Journals(context.Context) ([]*nodev1.RuntimeJournalIdentity, error)
}

func (w localRuntime) get(ctx context.Context, path string, message proto.Message, maximum int64) (bool, error) {
	return w.getJournal(ctx, path, message, maximum, nil)
}

func (w localRuntime) getJournal(ctx context.Context, path string, message proto.Message, maximum int64, journal *nodev1.RuntimeJournalIdentity) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://runtime"+path, nil)
	if err != nil {
		return false, err
	}
	if journal != nil {
		request.Header.Set("X-Pui-State-Journal", journal.StateJournalId)
		request.Header.Set("X-Pui-Credit-Journal", journal.CreditJournalId)
	}
	response, err := w.client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("runtime returned HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return false, err
	}
	if int64(len(encoded)) > maximum {
		return false, authority.ErrReport
	}
	return true, proto.Unmarshal(encoded, message)
}

func (l *LocalRuntimes) Journals(ctx context.Context) ([]*nodev1.RuntimeJournalIdentity, error) {
	journals := make([]*nodev1.RuntimeJournalIdentity, 0, len(l.workers))
	for _, worker := range l.workers {
		journal := new(nodev1.RuntimeJournalIdentity)
		present, err := worker.get(ctx, "/v1/journals", journal, 4096)
		if err != nil {
			return nil, err
		}
		if !present || !authority.ValidRuntime(journal) || journal.RuntimeId != worker.endpoint.ID || journal.CoreId != worker.endpoint.Core {
			return nil, state.ErrIdentity
		}
		journals = append(journals, journal)
	}
	id := l.store.Identity()
	registration, _, err := authority.NormalizeRegistration(&nodev1.JournalRegistration{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: l.store.JournalID(), Runtimes: journals})
	if err != nil {
		return nil, err
	}
	if err := l.store.BindRuntimeJournals(registration.Runtimes); err != nil {
		return nil, err
	}
	return registration.Runtimes, nil
}

func (a *Agent) registerJournals(ctx context.Context, conn grpc.ClientConnInterface) error {
	provider, ok := a.applier.(JournalProvider)
	if !ok {
		return authority.ErrReport
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	journals, err := provider.Journals(ctx)
	if err != nil {
		return err
	}
	id := a.store.Identity()
	request, digest, err := authority.NormalizeRegistration(&nodev1.JournalRegistration{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: a.store.JournalID(), Runtimes: journals})
	if err != nil {
		return err
	}
	if err := a.store.BindRuntimeJournals(request.Runtimes); err != nil {
		return err
	}
	receipt, err := nodev1.NewAuthorityServiceClient(conn).RegisterJournals(ctx, request)
	if err != nil {
		return err
	}
	if receipt.AgentStateJournalId != request.AgentStateJournalId || !bytes.Equal(receipt.RegistrationSha256, digest) {
		return authority.ErrReport
	}
	return nil
}

func (l *LocalRuntimes) forwardAuthority(ctx context.Context, client nodev1.AuthorityServiceClient) error {
	return l.runtimePass(ctx, func(ctx context.Context, worker localRuntime) error {
		journal, err := l.bindWorkerJournal(ctx, worker)
		if err != nil {
			return err
		}
		report := new(nodev1.AuthorityReport)
		present, err := worker.get(ctx, "/v1/authority", report, state.MaxBatchBytes)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		id := l.store.Identity()
		if authority.ValidateReport(report, false) != nil || report.NodeId != id.NodeID || report.InstallationId != id.InstallationID || report.RuntimeId != journal.RuntimeId || report.StateJournalId != journal.StateJournalId || report.CreditJournalId != journal.CreditJournalId {
			return state.ErrIdentity
		}
		report.AgentStateJournalId = l.store.JournalID()
		receipt, err := client.Report(ctx, report)
		if err != nil {
			return err
		}
		if receipt.RuntimeId != report.RuntimeId || receipt.StateJournalId != report.StateJournalId || receipt.CreditJournalId != report.CreditJournalId || receipt.Sequence != report.Sequence {
			return authority.ErrReport
		}
		if err := worker.post(ctx, "/v1/authority/ack", receipt); err != nil {
			return err
		}
		return nil
	})
}

func (a *Agent) Authority(ctx context.Context) error {
	local, ok := a.applier.(*LocalRuntimes)
	if !ok {
		return authority.ErrReport
	}
	conn, err := a.connection()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := nodev1.NewAuthorityServiceClient(conn)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		err := local.forwardAuthority(ctx, client)
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
