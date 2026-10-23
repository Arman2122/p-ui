package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/inventory"
)

type capabilityEngine struct {
	creditEngine
	value *nodev1.CoreCapability
	err   error
	calls int
}

func (e *capabilityEngine) Capability(context.Context) (*nodev1.CoreCapability, error) {
	e.calls++
	return e.value, e.err
}

func TestCapabilityEndpointFencesJournalsBeforeProbingAndPreservesReadiness(t *testing.T) {
	f := newAuthorityFixture(t)
	engine := &capabilityEngine{value: &nodev1.CoreCapability{CoreId: "xray", Version: "sha256:" + strings.Repeat("a", 64), Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
	f.worker.backend.(*streamBackend).engine = engine
	f.worker.applying.Store(true)
	f.worker.restoreFailed.Store(true)
	f.worker.available.Store(false)
	call := func(state, credit string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
		request.Header.Set("X-Pui-State-Journal", state)
		request.Header.Set("X-Pui-Credit-Journal", credit)
		response := httptest.NewRecorder()
		f.worker.Handler().ServeHTTP(response, request)
		return response
	}
	for _, pair := range [][2]string{{"", ""}, {f.store.JournalID(), "replacement"}, {"replacement", f.credit.JournalID()}} {
		if response := call(pair[0], pair[1]); response.Code != http.StatusConflict || engine.calls != 0 {
			t.Fatalf("foreign journals reached probe: %d calls=%d", response.Code, engine.calls)
		}
	}
	response := call(f.store.JournalID(), f.credit.JournalID())
	var snapshot inventory.Snapshot
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil || snapshot.Journal == nil || snapshot.Journal.StateJournalId != f.store.JournalID() || snapshot.NodeID != f.id.NodeID || snapshot.InstallationID != f.id.InstallationID || snapshot.ControlPlaneID != f.id.ControlPlaneID || snapshot.Capability == nil || !snapshot.Capability.ActivationAdmission {
		t.Fatalf("installed inventory depended on transient graph readiness: %d %s", response.Code, response.Body.String())
	}
	if !f.worker.applying.Load() || !f.worker.restoreFailed.Load() || f.worker.available.Load() {
		t.Fatal("inventory probe changed serving readiness")
	}
	engine.err = inventory.ErrInventory
	if response := call(f.store.JournalID(), f.credit.JournalID()); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed actual probe advertised capability: %d", response.Code)
	}
	engine.err = nil
	engine.value.CoreId = "ssh"
	if response := call(f.store.JournalID(), f.credit.JournalID()); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("foreign core capability was relabeled: %d", response.Code)
	}
}
