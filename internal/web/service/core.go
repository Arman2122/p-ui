package service

import (
	"context"
	"sort"

	"github.com/Arman2122/p-ui/internal/core"
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
	// ClientCredentials are the credential fields a client of each kind carries,
	// keyed by kind. A kind absent here declares none; the form keeps its own.
	ClientCredentials map[string][]string `json:"clientCredentials" example:"{\"vless\":[\"uuid\"],\"vmess\":[\"uuid\",\"security\"]}"`
	// Shaping names the kernel key each kind's clients carry, so the client form
	// gates the speed-limit fields on what a core declares rather than on a
	// protocol ladder of its own. A kind absent here cannot be rate limited at
	// all, and the form says so instead of offering a field that does nothing.
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
		credentials := make(map[string][]string, len(kinds))
		shaping := make(map[string]string, len(kinds))
		for _, kind := range kinds {
			names = append(names, string(kind))
			if bound.Shape != nil {
				if selector := bound.Shape.ShapingSelector(kind); selector != core.SelectorNone {
					shaping[string(kind)] = string(selector)
				}
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
		unavailable := ""
		if err := bound.Core.Preflight(ctx); err != nil {
			unavailable = err.Error()
		}
		views = append(views, CoreView{
			ID:                string(described.ID),
			TitleKey:          described.TitleKey,
			Kinds:             names,
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
