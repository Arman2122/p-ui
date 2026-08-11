package egress

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Egress is one row of the egress table as this package needs it, not the GORM
// model: the manager is a leaf and the data layer must not be reachable from it.
type Egress struct {
	ID     int
	Type   string
	Enable bool
	// Target is the outbound or balancer tag the injected front sends traffic to.
	// It is resolved by the core's own config builder, never here.
	Target string
	// Settings is the per-type column that buys the next driver zero migrations.
	Settings json.RawMessage
	// Ingress names the devices whose traffic this egress claims, one `iif`
	// selector each. This package never learns what kind of device they are.
	Ingress []string
}

// Fill is what a driver puts in its egress's routing table besides the blackhole
// every table gets.
type Fill struct {
	// Device is the front the table's default route points at. It may legitimately be
	// absent: an Xray-owned tun exists only while Xray does, which is what fails closed.
	Device string
	// Sysctls are the knobs the front's return path needs, applied once the device
	// exists. Keyed by the full dotted name so the plane stays device-agnostic.
	Sysctls map[string]string
}

// Injection is what an egress contributes to a core's generated config. Raw JSON
// rather than a core's own type: this package must not import the Xray vendor.
type Injection struct {
	// Tag is the inbound tag a routing rule selects on. It matches no inbound row, so
	// the core's counters for it are discarded rather than billed a second time.
	Tag string
	// Inbound is one complete inbound object, ready to append to the config.
	Inbound json.RawMessage
}

// Driver answers what fills an egress type's routing table, and nothing else.
type Driver interface {
	Type() string
	Fill(e Egress) (Fill, error)
}

// Injector is the optional half: a driver whose front is a device the core itself
// creates. An ikev2 driver implements Driver alone — strongSwan makes its own.
type Injector interface {
	Inject(e Egress) (Injection, error)
}

// Registry maps an egress type to its driver. Registration is explicit, never
// init(): with init() the registered set depends on whichever main links it.
type Registry struct {
	mu      sync.RWMutex
	byType  map[string]Driver
	ordered []string
}

func NewRegistry() *Registry { return &Registry{byType: map[string]Driver{}} }

// Register claims one type. A duplicate is an error rather than a silent
// overwrite, which is how database/sql treats it and how sing-box gets it wrong.
func (r *Registry) Register(d Driver) error {
	if d == nil {
		return fmt.Errorf("egress: Register(nil)")
	}
	name := d.Type()
	if name == "" {
		return fmt.Errorf("egress: a driver registered an empty type")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.byType[name]; taken {
		return fmt.Errorf("%w: %q", ErrDuplicateDriver, name)
	}
	r.byType[name] = d
	r.ordered = append(r.ordered, name)
	return nil
}

// For returns the driver serving a type. A false second result means the manager
// must contain it, never let its users out through the server's own identity.
func (r *Registry) For(name string) (Driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.byType[name]
	return d, ok
}

// Types returns every registered type, sorted, so anything generated from it is
// byte-stable across builds.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.ordered))
	copy(out, r.ordered)
	sort.Strings(out)
	return out
}
