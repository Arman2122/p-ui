package worker

import (
	"net/http"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

func (w *Worker) RequireClock(clock *trustedtime.Clock) error {
	backend, ok := w.backend.(interface{ SetClock(limits.Clock) })
	if clock == nil || !ok {
		return trustedtime.ErrUntrusted
	}
	backend.SetClock(clock)
	w.clock = clock
	return nil
}

func (w *Worker) clockRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/time", func(response http.ResponseWriter, request *http.Request) {
		if w.clock == nil {
			http.Error(response, "runtime time authority unavailable", http.StatusServiceUnavailable)
			return
		}
		nonce, err := w.clock.Begin()
		if err != nil {
			http.Error(response, "runtime time authority unavailable", http.StatusServiceUnavailable)
			return
		}
		id := w.store.Identity()
		data, err := proto.Marshal(&nodev1.TimeRequest{NodeId: id.NodeID, InstallationId: id.InstallationID, Nonce: nonce})
		if err != nil {
			http.Error(response, "runtime time authority unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/protobuf")
		_, _ = response.Write(data)
	})
	mux.HandleFunc("POST /v1/time", func(response http.ResponseWriter, request *http.Request) {
		message := new(nodev1.TimeResponse)
		if err := readMessage(response, request, message, 1024); err != nil {
			http.Error(response, "invalid time response", http.StatusBadRequest)
			return
		}
		id := w.store.Identity()
		needsTime := false
		if w.clock != nil {
			_, err := w.clock.Now()
			needsTime = err != nil
		}
		if w.clock == nil || message.NodeId != id.NodeID || message.InstallationId != id.InstallationID || w.clock.Accept(message.Nonce, message.ServerUnixMilli) != nil {
			http.Error(response, "time response rejected", http.StatusConflict)
			return
		}
		if needsTime && !w.available.Load() {
			if desired, err := w.store.Desired(); err == nil && desired != nil {
				if err := w.Apply(request.Context(), desired); err != nil {
					w.restoreFailed.Store(true)
				}
			}
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
