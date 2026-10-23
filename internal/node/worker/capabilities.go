package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func (s *streamBackend) Capability(ctx context.Context) (*nodev1.CoreCapability, error) {
	if source, ok := s.engine.(inventory.Source); ok {
		return source.Capability(ctx)
	}
	return nil, nil
}

func (w *Worker) capabilityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/capabilities", func(response http.ResponseWriter, request *http.Request) {
		if !w.matchesJournals(request) {
			http.Error(response, "runtime journal identity rejected", http.StatusConflict)
			return
		}
		source, ok := w.backend.(inventory.Source)
		if !ok || w.core == "native" {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
		defer cancel()
		capability, err := source.Capability(ctx)
		if err == nil && capability == nil {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			http.Error(response, "runtime inventory unavailable", http.StatusServiceUnavailable)
			return
		}
		capability, err = inventory.Normalize(capability)
		if err != nil || capability.CoreId != w.core {
			http.Error(response, "runtime inventory invalid", http.StatusServiceUnavailable)
			return
		}
		journal, err := w.journals()
		if err != nil || !w.matchesJournals(request) || ctx.Err() != nil {
			http.Error(response, "runtime inventory unavailable", http.StatusServiceUnavailable)
			return
		}
		id := w.store.Identity()
		snapshot := inventory.Snapshot{NodeID: id.NodeID, InstallationID: id.InstallationID, ControlPlaneID: id.ControlPlaneID, Journal: journal, Capability: capability}
		encoded, err := json.Marshal(snapshot)
		if err != nil || len(encoded) > inventory.MaxResponseBytes {
			http.Error(response, "runtime inventory unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(encoded)
	})
}
