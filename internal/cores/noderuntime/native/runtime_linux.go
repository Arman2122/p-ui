//go:build linux

package nativeruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/awg"
	ocserv "github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	openvpn "github.com/Arman2122/p-ui/internal/cores/internal/openvpn"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/native"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
	"github.com/Arman2122/p-ui/internal/node/worker"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

type Runtime struct {
	mu             sync.Mutex
	control        *native.Controller
	plane          *guardedPlane
	manager        *engine.Manager
	amneziaPlane   *guardedPlane
	amneziaManager *engine.Manager
	fingerprint    [32]byte
	configured     bool
	openvpn        *openVPNManager
	openvpnConfig  []openvpn.NodeConfiguration
	grants         []governor.Grant
	ocserv         *ocservManager
	ocservConfig   []ocserv.NodeConfiguration
	assignments    []native.Assignment
	selectors      map[string][]native.Selector
	byClient       map[string]governor.Grant
	clock          limits.Clock
	journal        *native.Journal
	initialized    bool
	authentication map[admission.Subject]*admission.BoundProof
}

func (r *Runtime) SetClock(clock limits.Clock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = clock
	r.control.SetClock(clock)
	r.openvpn.clock, r.ocserv.clock = clock, clock
	for _, process := range r.openvpn.processes {
		process.auth.SetClock(clock)
	}
	for _, process := range r.ocserv.processes {
		process.auth.SetClock(clock)
	}
}

func (r *Runtime) authorizationTime() (time.Time, error) {
	if r.clock != nil {
		return r.clock.Now()
	}
	return time.Now(), nil
}

func Open(ctx context.Context, dir, id string, store *state.Store, config credentials.Config) (worker.Backend, io.Closer, error) {
	if os.Geteuid() != 0 || id != "native" {
		return nil, nil, errors.New("native runtime requires the dedicated root host service and identity native")
	}
	protected, err := (&guardedPlane{management: config}).managementAddresses()
	if err != nil {
		return nil, nil, err
	}
	journal, err := native.Open(filepath.Join(dir, "credit.db"))
	if err != nil {
		return nil, nil, err
	}
	gate, err := native.NewLinux(store.Identity().InstallationID, journal.JournalID())
	if err != nil {
		_ = journal.Close()
		return nil, nil, err
	}
	control, err := native.NewController(journal, store, id, gate)
	network, networkErr := journal.Network()
	binding, initialized, targetErr := network.Target()
	err = errors.Join(err, networkErr, targetErr)
	if err == nil && initialized {
		if protected[binding.PeerIPv4] || protected[binding.GatewayIPv4] {
			err = native.ErrOwnership
		} else {
			err = gate.ConfigureNetwork(network)
		}
	}
	if err == nil && !initialized {
		records, readErr := journal.Records()
		err = readErr
		if err == nil && len(records) != 0 {
			err = native.ErrOwnership
		}
	}
	var clock *trustedtime.Clock
	if err == nil {
		var source trustedtime.Source
		source, err = trustedtime.SystemSource()
		if err == nil {
			clock, err = trustedtime.New(store, source)
			if err == nil {
				control.SetClock(clock)
				if _, clockErr := clock.Now(); clockErr != nil {
					err = gate.Quarantine(ctx)
				}
			}
		}
	}
	if err == nil && initialized {
		err = gate.Initialize(ctx)
	}
	if err == nil && initialized {
		err = control.Recover(ctx)
	}
	if err == nil && initialized {
		err = journal.CommitNetwork(binding)
		if err == nil {
			err = gate.ConfigureNetwork(native.NetworkState{Current: &binding})
		}
	}
	if err != nil {
		_ = journal.Close()
		return nil, nil, err
	}
	plane := &guardedPlane{Plane: engine.NewKernelPlane("wireguard", wireguardDriver{}), gate: gate, owner: "penhoon-node-v2:" + store.Identity().InstallationID, kind: "wireguard", prefix: "phvw"}
	plane.management = config
	amneziaPlane := &guardedPlane{Plane: awg.NewPlane(), gate: gate, owner: plane.owner, management: config, kind: awg.LinkType, prefix: "phva"}
	openVPNPlane := &guardedPlane{gate: gate, owner: plane.owner, management: config, kind: "tuntap", prefix: "phvo"}
	ocservPlane := &guardedPlane{gate: gate, owner: plane.owner, management: config, kind: "tuntap", prefix: "phvc"}
	runtime := &Runtime{control: control, plane: plane, manager: engine.NewNamedManager(plane, "phvw"), amneziaPlane: amneziaPlane, amneziaManager: engine.NewNamedManager(amneziaPlane, "phva"), openvpn: newOpenVPNManager(ctx, openVPNPlane), ocserv: newOcservManager(ctx, ocservPlane), journal: journal, initialized: initialized}
	runtime.SetClock(clock)
	return runtime, journal, nil
}

func (r *Runtime) Validate(runtime *configuration.Runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	config, err := compile(runtime)
	if err != nil {
		return err
	}
	if err := r.validateNetwork(config.config.Network); err != nil {
		return err
	}
	if err := r.plane.preflight(config.instances); err != nil {
		return err
	}
	if len(config.amnezia) > 0 {
		if err := r.amneziaPlane.Probe(context.Background()); err != nil {
			return err
		}
	}
	if err := r.amneziaPlane.preflight(config.amnezia); err != nil {
		return err
	}
	if err := r.openvpn.preflight(config.config.OpenVPN); err != nil {
		return err
	}
	return r.ocserv.preflight(config.config.OpenConnect)
}

func (r *Runtime) Apply(ctx context.Context, runtime *configuration.Runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	config, err := compile(runtime)
	if err != nil {
		return err
	}
	binding, authentication, err := captureNativeAuthentication(ctx, runtime)
	if err != nil {
		return err
	}
	if err := r.validateNetwork(config.config.Network); err != nil {
		return err
	}
	if err := r.plane.preflight(config.instances); err != nil {
		return err
	}
	if err := r.amneziaPlane.preflight(config.amnezia); err != nil {
		return err
	}
	if err := r.openvpn.preflight(config.config.OpenVPN); err != nil {
		return err
	}
	if err := r.ocserv.preflight(config.config.OpenConnect); err != nil {
		return err
	}
	var grants []governor.Grant
	if runtime != nil {
		grants = runtime.Grants
	}
	encoded, err := json.Marshal(config.config)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(encoded)
	r.openvpn.binding, r.ocserv.binding = binding, binding
	if !r.configured || r.fingerprint != fingerprint || !maps.Equal(r.authentication, authentication) {
		if err := r.control.Quiesce(ctx); err != nil {
			return err
		}
		r.configured = false
		r.openvpnConfig = nil
		r.ocservConfig = nil
		if err := r.openvpn.stop(); err != nil {
			return err
		}
		if err := r.ocserv.stop(); err != nil {
			return err
		}
		if err := r.applyNetwork(ctx, config.config.Network); err != nil {
			return err
		}
		if !r.initialized {
			return nil
		}
		if err := r.manager.Reconcile(ctx, config.instances); err != nil {
			return err
		}
		if err := r.amneziaManager.Reconcile(ctx, config.amnezia); err != nil {
			return err
		}
		for _, instance := range config.config.AmneziaWG {
			if err := awg.ConfigureParams(r.amneziaManager.Name(int(instance.ID)), instance.Parameters); err != nil {
				return err
			}
		}
		if err := r.openvpn.reconcile(ctx, config.config.OpenVPN, grants); err != nil {
			return err
		}
		if len(config.config.OpenConnect) > 0 {
			if err := r.ocserv.listen(httpHandler(r)); err != nil {
				return err
			}
			if err := r.ocserv.startMissing(ctx, config.config.OpenConnect, grants); err != nil {
				return err
			}
		}
		r.fingerprint, r.configured, r.authentication = fingerprint, true, authentication
	}
	r.openvpnConfig, r.grants = config.config.OpenVPN, grants
	r.ocservConfig, r.assignments = config.config.OpenConnect, config.assignments
	r.selectors = config.selectors
	r.byClient = make(map[string]governor.Grant, len(grants))
	for _, grant := range grants {
		r.byClient[grant.ClientID] = grant
	}
	if err := r.openvpn.reconcile(ctx, r.openvpnConfig, r.grants); err != nil {
		return err
	}
	for _, process := range r.ocserv.processes {
		process.auth.Grants(grants)
	}
	return r.applyNativeAssignments(ctx)
}

func (r *Runtime) Collect(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.initialized {
		return nil
	}
	var failures []error
	if r.configured {
		applied, err := r.control.ResumeAuthority(ctx)
		r.cacheAuthority(applied)
		failures = append(failures, err)
	}
	if r.configured && r.ocserv.prune() {
		if err := r.applyNativeAssignments(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if err := r.control.Collect(ctx); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if err := r.plane.gate.PruneRoutes(ctx); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if r.configured {
		if err := r.openvpn.running(ctx, r.openvpnConfig, r.grants); err != nil {
			return errors.Join(append(failures, err)...)
		}
		failures = append(failures, r.ocserv.startMissing(ctx, r.ocservConfig, r.grants))
	}
	return errors.Join(failures...)
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configured = false
	err := errors.Join(r.control.Quiesce(ctx), r.openvpn.stop(), r.ocserv.stop())
	if r.ocserv.server != nil {
		err = errors.Join(err, r.ocserv.server.Close())
		r.ocserv.server = nil
	}
	return err
}

func (r *Runtime) grant(id string) (governor.Grant, bool) {
	grant, ok := r.byClient[id]
	return grant, ok
}

func (r *Runtime) applyNativeAssignments(ctx context.Context) error {
	r.ocserv.pruneBindings()
	assignments := make([]native.Assignment, len(r.assignments))
	indexes := make(map[string]int, len(assignments))
	for i, assignment := range r.assignments {
		assignments[i] = assignment
		assignments[i].Selectors = slices.Clone(assignment.Selectors)
		indexes[assignment.Grant.ClientID] = i
	}
	for _, binding := range r.ocserv.bindings {
		index, ok := indexes[binding.ClientID]
		if !ok {
			return native.ErrAuthority
		}
		assignments[index].Selectors = append(assignments[index].Selectors, native.Selector{Device: binding.Device, Address: binding.Address})
	}
	for i := range assignments {
		sortNativeSelectors(assignments[i].Selectors)
	}
	return r.control.Reconcile(ctx, assignments)
}

func sortNativeSelectors(selectors []native.Selector) {
	sort.Slice(selectors, func(a, b int) bool {
		left, right := selectors[a], selectors[b]
		if left.Device != right.Device {
			return left.Device < right.Device
		}
		return left.Address.Compare(right.Address) < 0
	})
}
