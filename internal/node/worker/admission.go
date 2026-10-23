package worker

import (
	"context"
	"net/http"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
)

func (w *Worker) admissionTime() (time.Time, error) {
	if w.clock == nil || !w.available.Load() || w.applying.Load() || w.restoreFailed.Load() {
		return time.Time{}, admission.ErrUnavailable
	}
	return w.clock.RequestTime()
}

func (w *Worker) admissionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admission", func(response http.ResponseWriter, request *http.Request) {
		if !w.matchesJournals(request) {
			http.Error(response, "runtime journal identity rejected", http.StatusConflict)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		message, err := w.admissions.Next(ctx)
		if err != nil {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		writeAuthority(response, message)
	})
	mux.HandleFunc("POST /v1/admission", func(response http.ResponseWriter, request *http.Request) {
		if !w.matchesJournals(request) {
			http.Error(response, "runtime journal identity rejected", http.StatusConflict)
			return
		}
		result := new(nodev1.ActivationResult)
		if readMessage(response, request, result, 4096) != nil || w.admissions.Complete(result) != nil {
			http.Error(response, "runtime admission no longer pending", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
