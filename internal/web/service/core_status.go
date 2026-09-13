package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
CoreStatusView is one core as the Cores page shows it: what runs, whether it
answers, and which build is installed. Polled — unlike CoreView, which the
frontend fetches once because it changes only with the binary.

State and Ready are core.Health.Overall's, so the page, the event pass and the
conformance suite agree on what degraded means; a core that reports no health
says "unknown" rather than guessing.
*/
type CoreStatusView struct {
	ID       string `json:"id" example:"openvpn"`
	TitleKey string `json:"titleKey" example:"cores.openvpn.title"`
	// Available is this host's Preflight answer and Unavailable is why not.
	Available   bool   `json:"available" example:"true"`
	Unavailable string `json:"unavailable,omitempty" example:"ocserv: stat /usr/sbin/ocserv: no such file (apt-get install ocserv)"`
	// State is running, degraded, stopped, failed, idle or unknown; Ready says
	// the daemon answers on its control surface; Detail is why not, or the last exit.
	State          string `json:"state" example:"running"`
	Ready          bool   `json:"ready" example:"true"`
	Detail         string `json:"detail,omitempty" example:"exit status 1: 2026-09-05T18:33:49+0000 ocserv[1234]: bind failed"`
	PID            int    `json:"pid,omitempty" example:"2046530"`
	Restarts       uint32 `json:"restarts,omitempty" example:"0"`
	SinceUnixMilli int64  `json:"since,omitempty" example:"1725000000000"`
	HealthError    string `json:"healthError,omitempty" example:"singbox: inbound 4: supervisor: list p-ui-singbox-4.service: dbus: connection closed"`
	// Instances are the per-inbound daemons of a core that runs one each; a
	// single-process core lists none and fills the fields above instead.
	Instances []InstanceStatusView `json:"instances"`
	Version   VersionStatusView    `json:"version"`
	// Logs says TailLog is available for this core, so the page offers the drawer.
	Logs bool `json:"logs" example:"true"`
	// TrafficSeenAt is when this core last reported traffic (Unix ms, 0 = not
	// since start) and TrafficError why the last poll failed, if it did.
	TrafficSeenAt int64  `json:"trafficSeenAt,omitempty" example:"1725000000000"`
	TrafficError  string `json:"trafficError,omitempty" example:"mtproto: stats unreadable for mtp-1"`
	// HostRuntime observes matching daemon processes without adopting them.
	HostRuntime *CoreHostRuntimeView `json:"hostRuntime,omitempty"`
}

type CoreHostProcessView struct {
	PID            int    `json:"pid" example:"2046530"`
	Executable     string `json:"executable" example:"/usr/sbin/openvpn"`
	State          string `json:"state" example:"running"`
	ParentPID      int    `json:"parentPid,omitempty" example:"2046529"`
	BinaryReplaced bool   `json:"binaryReplaced,omitempty" example:"true"`
}

type CoreHostRuntimeView struct {
	State     string                `json:"state" example:"running"`
	Processes []CoreHostProcessView `json:"processes"`
	Error     string                `json:"error,omitempty"`
}

// InstanceStatusView is one daemon of a per-inbound core.
type InstanceStatusView struct {
	InstanceID     int    `json:"instanceId" example:"4"`
	Tag            string `json:"tag" example:"ocs-4"`
	State          string `json:"state" example:"running"`
	Ready          bool   `json:"ready" example:"true"`
	Detail         string `json:"detail,omitempty" example:"the management socket is not open yet"`
	PID            int    `json:"pid,omitempty" example:"2046530"`
	Restarts       uint32 `json:"restarts,omitempty" example:"2"`
	SinceUnixMilli int64  `json:"since,omitempty" example:"1725000000000"`
}

// VersionStatusView is what is installed and whether the panel may change it.
// Reported is false for a core that names no version at all.
type VersionStatusView struct {
	Installed  string `json:"installed,omitempty" example:"1.2.4"`
	Reported   bool   `json:"reported" example:"true"`
	Manageable bool   `json:"manageable" example:"false"`
	Error      string `json:"error,omitempty" example:"core: daemon not installed"`
}

// coreStatusBudget bounds one core's probes; a hung daemon must not hold the page.
const coreStatusBudget = 4 * time.Second

// CoreStatusViews reads every registered core, in parallel, each under its own
// budget; the registry is read per call because the router is built before it.
func CoreStatusViews(ctx context.Context) []CoreStatusView {
	manager := runtime.GetManager()
	if manager == nil {
		return []CoreStatusView{}
	}
	return coreStatusViews(ctx, manager.Cores())
}

func coreStatusViews(ctx context.Context, reg *core.Registry) []CoreStatusView {
	if reg == nil {
		return []CoreStatusView{}
	}
	bounds := reg.Cores()
	views := make([]CoreStatusView, len(bounds))
	var wg sync.WaitGroup
	for i, bound := range bounds {
		wg.Add(1)
		go func(i int, bound *core.Bound) {
			defer wg.Done()
			views[i] = coreStatusView(ctx, bound)
		}(i, bound)
	}
	wg.Wait()
	hosts := observeCoreHostRuntimes(ctx)
	for i := range views {
		views[i].HostRuntime = hosts[views[i].ID]
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}

func coreStatusView(ctx context.Context, bound *core.Bound) CoreStatusView {
	ctx, cancel := context.WithTimeout(ctx, coreStatusBudget)
	defer cancel()
	described := bound.Core.Describe()
	v := CoreStatusView{ID: string(described.ID), TitleKey: described.TitleKey, State: "unknown", Instances: []InstanceStatusView{}, Logs: bound.Logs != nil}
	if err := bound.Core.Preflight(ctx); err != nil {
		v.Unavailable = err.Error()
	}
	v.Available = v.Unavailable == ""
	if bound.Health != nil {
		h, err := bound.Health.Health(ctx)
		if err != nil {
			v.HealthError = err.Error()
		} else {
			state, ready := h.Overall()
			v.State, v.Ready, v.Detail, v.PID, v.Restarts, v.SinceUnixMilli = string(state), ready, h.Detail, h.PID, h.Restarts, h.SinceUnixMilli
			for _, i := range h.Instances {
				v.Instances = append(v.Instances, InstanceStatusView{
					InstanceID: i.InstanceID, Tag: i.Tag, State: string(i.State), Ready: i.Ready, Detail: i.Detail,
					PID: i.PID, Restarts: i.Restarts, SinceUnixMilli: i.SinceUnixMilli,
				})
			}
		}
	}
	v.Version = VersionStatusView{Reported: bound.Version != nil, Manageable: bound.Versions != nil}
	if bound.Version != nil {
		switch installed, err := bound.Version.Installed(ctx); {
		case errors.Is(err, core.ErrNotInstalled):
			v.Version.Error = err.Error()
		case err != nil:
			v.Version.Error = err.Error()
		default:
			v.Version.Installed = installed
		}
	}
	seen, trafficErr := CoreTrafficHealth(v.ID)
	if !seen.IsZero() {
		v.TrafficSeenAt = seen.UnixMilli()
	}
	v.TrafficError = trafficErr
	return v
}
