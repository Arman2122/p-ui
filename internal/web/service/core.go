package service

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// CoreView is one registered core as the UI sees it. Kinds is []string rather
// than []core.Kind: openapigen emits none of the core contract's own type names.
type CoreView struct {
	ID       string `json:"id" example:"xray"`
	TitleKey string `json:"titleKey" example:"cores.xray.title"`
	// Kinds are the inbound protocols this core serves; a core may serve many,
	// so the id is not a protocol and must never be used as one.
	Kinds []string          `json:"kinds" example:"[\"vless\",\"vmess\"]"`
	Caps  core.Capabilities `json:"caps"`
	// ExitKinds are the kinds a route may terminate on — the outbound half of
	// Kinds, so an outbound picker is built from the registry rather than a list.
	ExitKinds []string `json:"exitKinds" example:"[\"wgkernel\"]"`
	// ClientCredentials are the credential fields a client of each kind carries,
	// keyed by kind. A kind absent here declares none; the form keeps its own.
	ClientCredentials map[string][]string `json:"clientCredentials" example:"{\"vless\":[\"uuid\"],\"vmess\":[\"uuid\",\"security\"]}"`
	// Shaping names the kernel key each kind's clients carry, so a form gates its
	// speed fields on this. A kind absent here cannot be rate limited at all.
	Shaping map[string]string `json:"shaping" example:"{\"wgkernel\":\"innerIP\"}"`
	/*
		SessionKinds are the kinds whose core can name each live connection and the
		address it came from — the one thing an IP limit is decided from.

		A kind absent here cannot have a cap enforced however the form is filled
		in: EvaluateIPLimits only ever sees clients a core reported a session for,
		and mtg, for one, reports a secret in use and no address at all. The form
		says so instead of accepting a number that does nothing. F120 / F302.
	*/
	SessionKinds []string `json:"sessionKinds" example:"[\"vless\",\"wgkernel\"]"`
	/*
		IpLimitEnforcement says, per kind, how a device over its cap is dealt with:
		"login" — refused at the core's own login and cut, no firewall needed;
		"cut" — cut by the core and dropped on the panel's ban set, since it admits
		at no login; "firewall" — the ban set alone. A kind absent here cannot be
		capped at all. The form gates the cap on this rather than on the firewall
		alone: a cap on an SSH client holds where the set cannot be written, and
		one on Xray does not.
	*/
	IpLimitEnforcement map[string]string `json:"ipLimitEnforcement,omitempty" example:"{\"ssh\":\"login\",\"vless\":\"firewall\"}"`
	// RateKinds are the kinds whose core throttles each client inside itself, so
	// a plan's rates reach them without a kernel identity. Shaping's complement.
	RateKinds []string `json:"rateKinds" example:"[\"ssh\"]"`
	// ApplyDelaySeconds, per kind, is how long an edit takes to land on a core
	// that applies on its next pass; a kind absent here applies at once.
	ApplyDelaySeconds map[string]int `json:"applyDelaySeconds" example:"{\"tuic\":10}"`
	/*
		PortlessKinds are the kinds that bind no port at all — an Xray TUN device
		is a kernel interface, not a listener.

		The form asks this instead of asking a protocol list, so it stops
		demanding a port for one, and the port-conflict check stops counting the
		stored value as occupied. Asked with no settings on purpose: whether a
		kind binds anything is not a property of one inbound's configuration. F319.
	*/
	PortlessKinds []string `json:"portlessKinds" example:"[\"tun\"]"`
	// Available is this host's Preflight answer and Unavailable is why not, so a
	// core the host cannot run is explained rather than silently offered.
	Available   bool   `json:"available" example:"true"`
	Unavailable string `json:"unavailable" example:"wireguard: no kernel support on this host"`
	// TrafficSeenAt is when this core last reported traffic (Unix ms, 0 = not
	// since start) and TrafficError why the last poll failed, if it did.
	TrafficSeenAt int64  `json:"trafficSeenAt,omitempty" example:"1725000000000"`
	TrafficError  string `json:"trafficError,omitempty" example:"mtproto: stats unreadable for mtp-1"`
}

// ipLimitEnforcementOf is the same choice enforcementFor makes per address, read
// off a kind's capabilities so the form can say it before a cap is ever set.
func ipLimitEnforcementOf(bound *core.Bound, kind core.Kind) string {
	switch {
	case bound.Cut != nil && bound.Admits != nil && bound.Admits.AdmitsAtLogin(kind):
		return "login"
	case bound.Cut != nil:
		return "cut"
	default:
		return "firewall"
	}
}

// CoreViews describes the cores this build can serve. The registry is read per
// call because the router is built before the panel wires one.
func CoreViews(ctx context.Context) []CoreView {
	manager := runtime.GetManager()
	if manager == nil {
		return []CoreView{}
	}
	return coreViews(ctx, manager.Cores())
}

// coreViews joins each core's descriptor with the kinds it claims — the one
// question the registry cannot answer on its own.
func coreViews(ctx context.Context, reg *core.Registry) []CoreView {
	if reg == nil {
		return []CoreView{}
	}
	bounds := reg.Cores()
	views := make([]CoreView, 0, len(bounds))
	for _, bound := range bounds {
		described := bound.Core.Describe()
		kinds := bound.Core.Kinds()
		names := make([]string, 0, len(kinds))
		exits := make([]string, 0, len(kinds))
		credentials := make(map[string][]string, len(kinds))
		shaping := make(map[string]string, len(kinds))
		// Per CORE, not per kind: Sessions answers for the whole process, so a
		// core either can report them for everything it serves or for nothing.
		sessions := make([]string, 0, len(kinds))
		enforcement := make(map[string]string, len(kinds))
		rates := make([]string, 0, len(kinds))
		delays := make(map[string]int, len(kinds))
		portless := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			names = append(names, string(kind))
			if bound.Sessions != nil {
				sessions = append(sessions, string(kind))
				enforcement[string(kind)] = ipLimitEnforcementOf(bound, kind)
			}
			if bound.Rate != nil && bound.Rate.EnforcesRates(kind) {
				rates = append(rates, string(kind))
			}
			if bound.Deferred != nil {
				if d := bound.Deferred.ApplyDelay(kind); d > 0 {
					delays[string(kind)] = int(d / time.Second)
				}
			}
			if bound.Transports != nil && bound.Transports.Transports(kind, "", "").None {
				portless = append(portless, string(kind))
			}
			if bound.Shape != nil {
				if selector := bound.Shape.ShapingSelector(kind); selector != core.SelectorNone {
					shaping[string(kind)] = string(selector)
				}
			}
			// Mirrors cores.ExitKinds, per core rather than build-wide, so the
			// picker can group a destination under the core that terminates it.
			if bound.Egress != nil && bound.Egress.ExitHandleKind(kind) != core.ExitNone {
				exits = append(exits, string(kind))
			}
			if bound.Creds == nil {
				continue
			}
			if fields := bound.Creds.ClientCredentials(kind); len(fields) > 0 {
				credentials[string(kind)] = fields
			}
		}
		// Both sources are declaration order — registration order here, the
		// core's own slice for kinds — and an unstable order makes gen-check flap.
		sort.Strings(names)
		sort.Strings(exits)
		sort.Strings(sessions)
		sort.Strings(rates)
		sort.Strings(portless)
		unavailable := ""
		if err := bound.Core.Preflight(ctx); err != nil {
			unavailable = err.Error()
		}
		seen, trafficErr := CoreTrafficHealth(string(described.ID))
		var seenAt int64
		if !seen.IsZero() {
			seenAt = seen.UnixMilli()
		}
		views = append(views, CoreView{
			ID:                 string(described.ID),
			TitleKey:           described.TitleKey,
			Kinds:              names,
			ExitKinds:          exits,
			Caps:               described.Caps,
			ClientCredentials:  credentials,
			Shaping:            shaping,
			SessionKinds:       sessions,
			IpLimitEnforcement: enforcement,
			RateKinds:          rates,
			ApplyDelaySeconds:  delays,
			PortlessKinds:      portless,
			Available:          unavailable == "",
			Unavailable:        unavailable,
			TrafficSeenAt:      seenAt,
			TrafficError:       trafficErr,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}

/*
ClientLocalAddress is one address a client answers to inside a tunnel, and the
core that reported it.

Per core rather than merged, because the answer differs per protocol and the two
are only comparable within one: two clients can reach each other over the address
they hold on the SAME inbound, and never across cores.
*/
type ClientLocalAddress struct {
	Core    string `json:"core" example:"wgkernel"`
	Address string `json:"address" example:"10.0.0.11"`
}

// ClientLocalAddresses names where one client sits inside each core's tunnel,
// read live: it is current state, not the address history /ips/:email serves.
func ClientLocalAddresses(ctx context.Context, email string) []ClientLocalAddress {
	manager := runtime.GetManager()
	if manager == nil {
		return []ClientLocalAddress{}
	}
	return clientLocalAddresses(ctx, manager.Cores(), email)
}

/*
clientLocalAddresses walks every core that can name its sessions.

Fail-open PER CORE, like ObserveSessions: a core that cannot be asked contributes
nothing and the others still answer, because one unreachable daemon blanking the
whole list would read as "this client has no address anywhere".
*/
func clientLocalAddresses(ctx context.Context, reg *core.Registry, email string) []ClientLocalAddress {
	out := []ClientLocalAddress{}
	if reg == nil || email == "" {
		return out
	}
	for _, bound := range reg.Cores() {
		if bound.Sessions == nil {
			continue
		}
		id := string(bound.Core.Describe().ID)
		sessions, err := bound.Sessions.Sessions(ctx)
		if err != nil {
			logger.Debug("core: ", id, " could not name its sessions for the address list: ", err)
			continue
		}
		for _, session := range sessions {
			if session.Email != email {
				continue
			}
			for _, addr := range session.Local {
				entry := ClientLocalAddress{Core: id, Address: addr.String()}
				// A core may report one session per connection, all on one address.
				if !slices.Contains(out, entry) {
					out = append(out, entry)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Core != out[j].Core {
			return out[i].Core < out[j].Core
		}
		return out[i].Address < out[j].Address
	})
	return out
}
