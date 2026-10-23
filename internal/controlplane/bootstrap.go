package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func NewEnrollmentHandler(store *Store, plane string, issuer *Issuer) (http.Handler, error) {
	if store == nil || !validID(plane) || issuer == nil || issuer.Certificate == nil || issuer.Signer == nil || len(issuer.TrustBundle) == 0 {
		return nil, ErrInvalid
	}
	slots := make(chan struct{}, 8)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil {
			http.Error(w, "HTTPS required", http.StatusUpgradeRequired)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "enrollment busy", http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			var maximum *http.MaxBytesError
			if errors.As(err, &maximum) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid request", http.StatusBadRequest)
			}
			return
		}
		request := new(nodev1.EnrollRequest)
		if err := protojson.Unmarshal(body, request); err != nil {
			http.Error(w, "invalid enrollment request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		response, err := store.Redeem(ctx, plane, issuer, request)
		if errors.Is(err, ErrEnrollment) || errors.Is(err, identity.ErrCSR) {
			http.Error(w, "enrollment rejected", http.StatusForbidden)
			return
		}
		if err != nil {
			http.Error(w, "enrollment temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		encoded, err := protojson.Marshal(response)
		if err != nil {
			http.Error(w, "enrollment response unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})
	mux := http.NewServeMux()
	mux.Handle("POST /enroll", handler)
	mux.Handle("POST /recover/challenge", recoveryHandler(store, plane, issuer, true, slots))
	mux.Handle("POST /recover", recoveryHandler(store, plane, issuer, false, slots))
	return mux, nil
}
