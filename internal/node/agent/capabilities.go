package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/inventory"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type CapabilityReporter interface {
	Capabilities(context.Context) []*nodev1.CoreCapability
}

func (l *LocalRuntimes) readCapability(ctx context.Context, worker localRuntime) (*nodev1.CoreCapability, error) {
	journal, err := l.bindWorkerJournal(ctx, worker)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://runtime/v1/capabilities", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Pui-State-Journal", journal.StateJournalId)
	request.Header.Set("X-Pui-Credit-Journal", journal.CreditJournalId)
	response, err := worker.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, inventory.ErrInventory
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, inventory.MaxResponseBytes+1))
	if err != nil || len(data) > inventory.MaxResponseBytes {
		return nil, inventory.ErrInventory
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot inventory.Snapshot
	if decoder.Decode(&snapshot) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, inventory.ErrInventory
	}
	id := l.store.Identity()
	if snapshot.NodeID != id.NodeID || snapshot.InstallationID != id.InstallationID || snapshot.ControlPlaneID != id.ControlPlaneID || !proto.Equal(snapshot.Journal, journal) {
		return nil, state.ErrIdentity
	}
	capability, err := inventory.Normalize(snapshot.Capability)
	if err != nil || capability.CoreId != journal.CoreId || ctx.Err() != nil {
		return nil, inventory.ErrInventory
	}
	return capability, nil
}

func (l *LocalRuntimes) Capabilities(ctx context.Context) []*nodev1.CoreCapability {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	results := make(map[string]*nodev1.CoreCapability, len(l.workers))
	var mu sync.Mutex
	_ = l.runtimePass(ctx, func(ctx context.Context, worker localRuntime) error {
		capability, err := l.readCapability(ctx, worker)
		if err == nil {
			mu.Lock()
			results[worker.endpoint.ID] = capability
			mu.Unlock()
		}
		return err
	})
	families := make(map[string]*nodev1.CoreCapability)
	blocked := make(map[string]bool)
	for _, worker := range l.workers {
		capability := results[worker.endpoint.ID]
		family := worker.endpoint.Core
		if capability == nil || families[family] != nil && !proto.Equal(families[family], capability) {
			blocked[family] = true
			continue
		}
		families[family] = capability
	}
	var capabilities []*nodev1.CoreCapability
	for family, capability := range families {
		if !blocked[family] {
			capabilities = append(capabilities, capability)
		}
	}
	result, _ := inventory.Canonical(capabilities)
	return result
}

func (a *Agent) capabilities(ctx context.Context) []*nodev1.CoreCapability {
	if reporter, ok := a.applier.(CapabilityReporter); ok {
		capabilities, err := inventory.Canonical(reporter.Capabilities(ctx))
		if err == nil {
			return capabilities
		}
	}
	return nil
}

func (a *Agent) hello(ctx context.Context) (*nodev1.NodeHello, error) {
	capabilities := a.capabilities(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cursors, err := a.store.Cursors()
	if err != nil {
		return nil, err
	}
	id := a.store.Identity()
	return &nodev1.NodeHello{
		NodeId: id.NodeID, InstallationId: id.InstallationID, ProtocolVersion: compatibility.Protocol,
		AuthorityGeneration: cursors.Authority, AppliedRevision: cursors.Applied, DesiredRevision: cursors.Desired,
		DurableUsageSequence: cursors.DurableUsage, AgentStateJournalId: a.store.JournalID(), Capabilities: capabilities,
	}, nil
}

func (a *Agent) inventoryChanged(ctx context.Context, previous []*nodev1.CoreCapability) bool {
	current := a.capabilities(ctx)
	return !slices.EqualFunc(previous, current, func(a, b *nodev1.CoreCapability) bool { return proto.Equal(a, b) })
}
