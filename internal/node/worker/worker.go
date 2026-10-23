package worker

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/assets"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

type Worker struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	id             string
	core           string
	backend        Backend
	store          *state.Store
	interval       time.Duration
	available      atomic.Bool
	applying       atomic.Bool
	restoreFailed  atomic.Bool
	clock          *trustedtime.Clock
	assetDirectory string
	assets         *assets.Store
	activeAssets   []string
	authorityScope *authorityScope
	admissions     *admission.Broker
}

func New(ctx context.Context, id, core string, engine Engine, store *state.Store, credit *governor.Store) (*Worker, error) {
	backend, err := NewStreamBackend(id, engine, store, credit)
	if err != nil {
		return nil, err
	}
	return NewWithBackend(ctx, id, core, store, backend, 2*time.Second)
}

func NewWithBackend(ctx context.Context, id, core string, store *state.Store, backend Backend, interval time.Duration) (*Worker, error) {
	if id == "" || core == "" || store == nil || backend == nil || interval < 10*time.Millisecond || interval > time.Minute {
		return nil, configuration.ErrConfiguration
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &Worker{ctx: ctx, cancel: cancel, id: id, core: core, store: store, backend: backend, interval: interval}
	w.admissions = admission.New(id, w.admissionTime)
	return w, nil
}

func (w *Worker) desired(payload []byte) (*configuration.Runtime, error) {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil, err
	}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID != w.id {
			continue
		}
		if runtime.Core != w.core {
			return nil, configuration.ErrConfiguration
		}
		resolved, err := w.resolveAssets(&runtime)
		if err != nil {
			return nil, err
		}
		if err := w.backend.Validate(resolved); err != nil {
			return nil, err
		}
		return resolved, nil
	}
	return nil, nil
}

func (w *Worker) Apply(ctx context.Context, deployment *nodev1.Deployment) error {
	return w.apply(ctx, deployment, "")
}

func (w *Worker) apply(ctx context.Context, deployment *nodev1.Deployment, agentJournal string) (result error) {
	if deployment == nil {
		return configuration.ErrConfiguration
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() {
		if err := w.pruneAssets(); err != nil {
			w.available.Store(false)
			result = errors.Join(result, err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if agentJournal != "" {
		if err := w.store.CheckAgentJournal(agentJournal); err != nil {
			return err
		}
	}
	runtime, err := w.desired(deployment.Payload)
	if err != nil {
		return err
	}
	runtime, scope, err := w.prepareAuthority(deployment, runtime)
	if err != nil {
		return err
	}
	possibleAssets, err := w.possibleActiveAssets(runtime)
	if err != nil {
		return err
	}
	if err := w.store.Receive(deployment); err != nil {
		return err
	}
	if agentJournal != "" {
		if err := w.store.BindAgentJournal(agentJournal); err != nil {
			return err
		}
	}
	w.available.Store(false)
	var descriptors []admission.Descriptor
	if runtime != nil {
		descriptors = runtime.Admissions
	}
	binding, err := w.admissions.Prepare(descriptors)
	if err != nil {
		return err
	}
	w.restoreFailed.Store(false)
	w.applying.Store(true)
	defer w.applying.Store(false)
	if w.clock != nil && runtime != nil {
		if _, err := w.clock.Now(); err != nil {
			return err
		}
	}
	w.activeAssets = possibleAssets
	if err := w.backend.Apply(admission.WithBinding(w.ctx, binding), runtime); err != nil {
		return err
	}
	w.activeAssets = runtimeAssetDigests(runtime)
	if err := w.store.Applied(deployment.AuthorityGeneration, deployment.Revision, deployment.PayloadSha256); err != nil {
		return err
	}
	if err := w.store.PruneAuthorityIntents(deployment.Revision, deployment.PayloadSha256); err != nil {
		return err
	}
	w.authorityScope = scope
	if err := w.admissions.Commit(deployment.Revision, binding); err != nil {
		return err
	}
	w.available.Store(true)
	return nil
}

func (w *Worker) Handler() http.Handler {
	mux := http.NewServeMux()
	w.clockRoutes(mux)
	w.capabilityRoutes(mux)
	w.authorityHandlers(mux)
	w.creditRoutes(mux)
	w.authorityUpdateRoutes(mux)
	w.admissionRoutes(mux)
	mux.HandleFunc("GET /v1/status", func(response http.ResponseWriter, request *http.Request) {
		cursors, err := w.store.Cursors()
		if err != nil {
			http.Error(response, "runtime journal unavailable", http.StatusServiceUnavailable)
			return
		}
		condition := &nodev1.RuntimeCondition{ResourceId: w.id, Condition: "CONFIGURATION_APPLIED", Ready: true}
		switch {
		case w.applying.Load():
			condition.Condition, condition.Ready = "CONFIGURATION_PENDING", false
		case w.restoreFailed.Load():
			condition.Condition, condition.Ready = "RESTORE_FAILED", false
		case !w.available.Load():
			condition.Condition, condition.Ready = "CONFIGURATION_FAILED", false
		case cursors.Desired != cursors.Applied:
			condition.Condition = "CONFIGURATION_PENDING"
			condition.Ready = false
		case cursors.Desired == 0:
			condition.Condition = "UNCONFIGURED"
		}
		encoded, err := proto.Marshal(condition)
		if err != nil {
			http.Error(response, "runtime status unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/protobuf")
		_, _ = response.Write(encoded)
	})
	mux.HandleFunc("POST /v1/validate", func(response http.ResponseWriter, request *http.Request) {
		if !w.matchesJournals(request) || !w.matchesAgentJournal(request) {
			http.Error(response, "runtime journal identity rejected", http.StatusConflict)
			return
		}
		deployment := new(nodev1.Deployment)
		if err := readMessage(response, request, deployment, 33<<20); err != nil {
			http.Error(response, "invalid deployment", http.StatusBadRequest)
			return
		}
		w.mu.Lock()
		_, err := w.desired(deployment.Payload)
		err = errors.Join(err, w.pruneAssets())
		w.mu.Unlock()
		if err != nil {
			http.Error(response, "invalid runtime configuration", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/apply", func(response http.ResponseWriter, request *http.Request) {
		if !w.matchesJournals(request) || !w.matchesAgentJournal(request) {
			http.Error(response, "runtime journal identity rejected", http.StatusConflict)
			return
		}
		deployment := new(nodev1.Deployment)
		if err := readMessage(response, request, deployment, 33<<20); err != nil {
			http.Error(response, "invalid deployment", http.StatusBadRequest)
			return
		}
		if err := w.apply(request.Context(), deployment, request.Header.Get("X-Pui-Agent-Journal")); err != nil {
			http.Error(response, "runtime application failed", http.StatusConflict)
			return
		}
		response.Header().Set(configuration.AssetManifestHeader, hex.EncodeToString(runtimeAssetDigest(w.id, deployment.Payload)))
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/usage", func(response http.ResponseWriter, request *http.Request) {
		batches, err := w.store.PendingUsage(1)
		if err != nil {
			http.Error(response, "usage journal unavailable", http.StatusServiceUnavailable)
			return
		}
		if len(batches) == 0 {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		encoded, err := proto.Marshal(batches[0])
		if err != nil {
			http.Error(response, "usage unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/protobuf")
		_, _ = response.Write(encoded)
	})
	mux.HandleFunc("POST /v1/ack", func(response http.ResponseWriter, request *http.Request) {
		receipt := new(nodev1.UsageReceipt)
		if err := readMessage(response, request, receipt, 1024); err != nil {
			http.Error(response, "invalid receipt", http.StatusBadRequest)
			return
		}
		if err := w.store.Acknowledge(receipt); err != nil {
			http.Error(response, "receipt rejected", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func readMessage(response http.ResponseWriter, request *http.Request, message proto.Message, maximum int64) error {
	data, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximum))
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, message)
}

func (w *Worker) Run(ctx context.Context, listener net.Listener) (result error) {
	defer func() {
		w.available.Store(false)
		_ = w.admissions.Replace(0, nil)
		w.cancel()
		w.mu.Lock()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result = errors.Join(result, w.backend.Shutdown(shutdown))
		if w.assets != nil {
			result = errors.Join(result, w.assets.Close())
		}
		w.mu.Unlock()
	}()
	defer listener.Close()
	desired, err := w.store.Desired()
	if err != nil {
		return err
	}
	if desired != nil {
		if err := w.Apply(ctx, desired); err != nil {
			w.restoreFailed.Store(true)
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			w.mu.Lock()
			stopErr := w.backend.Shutdown(cleanup)
			if stopErr == nil {
				w.activeAssets = nil
			}
			w.mu.Unlock()
			cancel()
			if stopErr != nil {
				return fmt.Errorf("stop rejected runtime: %w", errors.Join(err, stopErr))
			}
		}
	} else {
		w.available.Store(true)
	}
	server := &http.Server{
		Handler: w.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.cancel()
			_ = server.Close()
			<-done
			return nil
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			w.mu.Lock()
			err := w.backend.Collect(ctx)
			w.mu.Unlock()
			if err != nil {
				_ = server.Close()
				<-done
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("runtime supervision or usage checkpoint failed: %w", err)
			}
		}
	}
}
