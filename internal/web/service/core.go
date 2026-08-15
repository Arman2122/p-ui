package service

import (
	"context"
	"slices"
	"sort"

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
	// Available is this host's Preflight answer and Unavailable is why not, so a
	// core the host cannot run is explained rather than silently offered.
	Available   bool   `json:"available" example:"true"`
	Unavailable string `json:"unavailable" example:"wireguard: no kernel support on this host"`
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
		for _, kind := range kinds {
			names = append(names, string(kind))
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
		unavailable := ""
		if err := bound.Core.Preflight(ctx); err != nil {
			unavailable = err.Error()
		}
		views = append(views, CoreView{
			ID:                string(described.ID),
			TitleKey:          described.TitleKey,
			Kinds:             names,
			ExitKinds:         exits,
			Caps:              described.Caps,
			ClientCredentials: credentials,
			Shaping:           shaping,
			Available:         unavailable == "",
			Unavailable:       unavailable,
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
