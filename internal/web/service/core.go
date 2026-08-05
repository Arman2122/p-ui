package service

import (
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
}

// CoreViews describes the cores this build can serve. The registry is read per
// call because the router is built before the panel wires one.
func CoreViews() []CoreView {
	manager := runtime.GetManager()
	if manager == nil {
		return []CoreView{}
	}
	return coreViews(manager.Cores())
}

// coreViews joins each core's descriptor with the kinds it claims — the one
// question the registry cannot answer on its own.
func coreViews(reg *core.Registry) []CoreView {
	if reg == nil {
		return []CoreView{}
	}
	bounds := reg.Cores()
	views := make([]CoreView, 0, len(bounds))
	for _, bound := range bounds {
		described := bound.Core.Describe()
		kinds := bound.Core.Kinds()
		names := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			names = append(names, string(kind))
		}
		// Both sources are declaration order — registration order here, the
		// core's own slice for kinds — and an unstable order makes gen-check flap.
		sort.Strings(names)
		views = append(views, CoreView{
			ID:       string(described.ID),
			TitleKey: described.TitleKey,
			Kinds:    names,
			Caps:     described.Caps,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}
