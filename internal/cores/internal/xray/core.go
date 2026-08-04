// Package xray adapts the Xray core to the contract. It is the opposite shape
// to mtproto: one process serves every inbound, and changes apply over gRPC.
package xray

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

// ID names the core. It is not a Kind: Xray answers nine protocols, and the
// registry keys on those, not on this.
const ID core.Kind = "xray"

// kinds are the protocols Xray serves. They are literals rather than model
// constants so a core never depends on the data layer; internal/arch pins this
// list against the Go protocol constants so a new protocol cannot be orphaned.
var kinds = []core.Kind{
	"vmess", "vless", "trojan", "shadowsocks",
	"wireguard", "hysteria", "http", "mixed", "tunnel",
}

// Deps are the panel-side facts the core cannot derive. BaseConfig returns the
// config with every section except inbounds already filled in: routing, DNS,
// policy and the API listener stay the panel's business.
type Deps struct {
	BaseConfig func() (*engine.Config, error)
}

// Core supervises the single Xray process and applies changes through its API.
type Core struct {
	deps Deps
	mgr  *engine.Manager

	mu      sync.Mutex
	api     *engine.XrayAPI
	apiPort int
}

func New(deps Deps) *Core {
	return &Core{deps: deps, mgr: engine.GetManager()}
}

func (c *Core) Kinds() []core.Kind { return slices.Clone(kinds) }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       ID,
		TitleKey: "cores.xray.title",
		Caps: core.Capabilities{
			UserHotAdd:   core.Yes(),
			PerUserStats: core.Yes(),
			OnlineUsers:  core.Yes(),
			// Xray has no per-user byte budget to push down; quota is enforced
			// by the panel revoking the user. Links are still the panel's.
			QuotaPushdown: core.No(),
			ShareLink:     core.No(),
		},
	}
}

func (c *Core) Preflight(_ context.Context) error { return engine.CheckBinary() }

// Reconcile converges the one Xray process on the desired inbound set. An
// unchanged config is left alone, a change that the core API can absorb is
// applied in place, and anything else is a restart.
func (c *Core) Reconcile(_ context.Context, desired []core.Instance) error {
	cfg, err := c.deps.BaseConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("xray: BaseConfig returned no config")
	}
	cfg.InboundConfigs = append(cfg.InboundConfigs, inboundsOf(desired)...)

	process := c.mgr.Current()
	if process != nil && process.IsRunning() {
		if process.GetConfig().Equals(cfg) {
			return nil
		}
		if c.hotApply(process, cfg) {
			return nil
		}
		_ = process.Stop()
	}

	process = engine.NewProcess(cfg)
	c.mgr.Replace(process)
	c.noteRestart()
	return process.Start()
}

func inboundsOf(desired []core.Instance) []core.InboundConfig {
	out := make([]core.InboundConfig, 0, len(desired))
	for _, inst := range desired {
		if inbound, ok := toInbound(inst); ok {
			out = append(out, inbound)
		}
	}
	slices.SortFunc(out, func(a, b core.InboundConfig) int {
		switch {
		case a.Tag < b.Tag:
			return -1
		case a.Tag > b.Tag:
			return 1
		}
		return 0
	})
	return out
}

func (c *Core) StopAll(_ context.Context) error {
	process := c.mgr.Current()
	if process == nil || !process.IsRunning() {
		return nil
	}
	return process.Stop()
}

// PlanChange classifies one inbound's change through the same diff the apply
// path uses, so the answer cannot drift from what Reconcile will actually do.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	oldIb, oldOK := toInbound(before)
	newIb, newOK := toInbound(after)
	if oldOK != newOK {
		return core.ActionRestart
	}
	if oldIb.Equals(&newIb) {
		return core.ActionNoop
	}
	oldCfg := &engine.Config{InboundConfigs: []core.InboundConfig{oldIb}}
	newCfg := &engine.Config{InboundConfigs: []core.InboundConfig{newIb}}
	if _, ok := engine.ComputeHotDiff(oldCfg, newCfg); ok {
		return core.ActionHotApply
	}
	return core.ActionRestart
}

// ApplyInstance converges one inbound through the same diff the reconcile path
// uses. It is deliberately not a handler replacement: swapping the handler drops
// that inbound's connections, while a diff that only adds and removes users
// leaves everyone else connected.
func (c *Core) ApplyInstance(_ context.Context, inst core.Instance) error {
	process := c.mgr.Current()
	if process == nil || !process.IsRunning() {
		return errors.New("xray: core is not running")
	}
	next := withInbound(process.GetConfig(), inst)
	if !c.hotApply(process, next) {
		return fmt.Errorf("xray: inbound %q cannot be applied in place", inst.Tag)
	}
	return nil
}

// DropInstance removes one inbound and leaves the rest of the config alone.
func (c *Core) DropInstance(_ context.Context, inst core.Instance) error {
	process := c.mgr.Current()
	if process == nil || !process.IsRunning() {
		return errors.New("xray: core is not running")
	}
	disabled := inst
	disabled.Enable = false
	next := withInbound(process.GetConfig(), disabled)
	if !c.hotApply(process, next) {
		return fmt.Errorf("xray: inbound %q cannot be removed in place", inst.Tag)
	}
	return nil
}

// withInbound returns cfg with inst's inbound spliced in by tag: replaced when
// it is already there, appended when it is new, dropped when it is disabled.
// Every other section is carried over untouched.
func withInbound(cfg *engine.Config, inst core.Instance) *engine.Config {
	next := *cfg
	inbound, keep := toInbound(inst)
	next.InboundConfigs = make([]core.InboundConfig, 0, len(cfg.InboundConfigs)+1)
	replaced := false
	for _, existing := range cfg.InboundConfigs {
		if existing.Tag != inst.Tag {
			next.InboundConfigs = append(next.InboundConfigs, existing)
			continue
		}
		if keep {
			next.InboundConfigs = append(next.InboundConfigs, inbound)
		}
		replaced = true
	}
	if !replaced && keep {
		next.InboundConfigs = append(next.InboundConfigs, inbound)
	}
	return &next
}

func (c *Core) AddUser(_ context.Context, inst core.Instance, user core.User) error {
	api, err := c.connect()
	if err != nil {
		return err
	}
	return api.AddUser(string(inst.Kind), inst.Tag, clientOf(user))
}

func (c *Core) RemoveUser(_ context.Context, inst core.Instance, email string) error {
	api, err := c.connect()
	if err != nil {
		return err
	}
	return api.RemoveUser(inst.Tag, email)
}

func (c *Core) CollectTraffic(_ context.Context) ([]core.TrafficDelta, error) {
	api, err := c.connect()
	if err != nil {
		return nil, err
	}
	_, clients, err := api.GetTraffic()
	if err != nil {
		return nil, err
	}
	out := make([]core.TrafficDelta, 0, len(clients))
	for _, ct := range clients {
		out = append(out, core.TrafficDelta{Email: ct.Email, Up: ct.Up, Down: ct.Down})
	}
	return out, nil
}

// OnlineEmails asks the core directly. Unlike a scraped counter this read is
// not destructive, so it needs no cached answer.
func (c *Core) OnlineEmails(_ context.Context) ([]string, error) {
	api, err := c.connect()
	if err != nil {
		return nil, err
	}
	users, err := api.GetOnlineUsers()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.Email)
	}
	return out, nil
}

// connect returns an API client bound to the running process, reconnecting when
// the port moves. The client is long-lived because its traffic counter is: a
// fresh one each call would re-baseline and bill nothing, forever.
func (c *Core) connect() (*engine.XrayAPI, error) {
	process := c.mgr.Current()
	if process == nil || !process.IsRunning() {
		return nil, errors.New("xray: core is not running")
	}
	port := process.GetAPIPort()
	if port <= 0 {
		return nil, errors.New("xray: core exposes no API port")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		c.api = &engine.XrayAPI{}
	}
	if c.apiPort != port {
		c.api.Close()
		if err := c.api.Init(port); err != nil {
			return nil, err
		}
		c.apiPort = port
	}
	return c.api, nil
}

// noteRestart tells the traffic counter the next reading starts from zero.
func (c *Core) noteRestart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != nil {
		c.api.NoteCoreRestart()
	}
}

func (c *Core) hotApply(process *engine.Process, newCfg *engine.Config) bool {
	diff, ok := engine.ComputeHotDiff(process.GetConfig(), newCfg)
	if !ok {
		return false
	}
	if diff.Empty() {
		process.SetConfig(newCfg)
		return true
	}
	api, err := c.connect()
	if err != nil {
		return false
	}
	if !engine.ApplyHotDiff(api, diff) {
		return false
	}
	process.SetConfig(newCfg)
	return true
}
