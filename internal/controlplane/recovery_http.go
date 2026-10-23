package controlplane

import (
	"context"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func recoveryHandler(store *Store, plane string, issuer *Issuer, challenge bool, slots chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil {
			http.Error(w, "HTTPS required", http.StatusUpgradeRequired)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "recovery busy", http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			http.Error(w, "invalid recovery request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var response proto.Message
		if challenge {
			request := new(nodev1.RecoveryChallengeRequest)
			if protojson.Unmarshal(body, request) != nil {
				http.Error(w, "invalid recovery request", http.StatusBadRequest)
				return
			}
			response, err = store.RecoveryChallenge(ctx, request)
		} else {
			request := new(nodev1.RecoveryRequest)
			if protojson.Unmarshal(body, request) != nil {
				http.Error(w, "invalid recovery request", http.StatusBadRequest)
				return
			}
			response, err = store.RecoverKey(ctx, plane, issuer, request)
		}
		if err != nil {
			http.Error(w, "recovery rejected", http.StatusForbidden)
			return
		}
		encoded, err := protojson.Marshal(response)
		if err != nil {
			http.Error(w, "recovery unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})
}
