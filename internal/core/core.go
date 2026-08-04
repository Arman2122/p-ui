package core

import "context"

// Kind identifies a protocol a core serves. It is the key the registry is keyed
// on and the value persisted in inbounds.protocol, so a Kind is never renamed or
// reused: old installs hold rows naming kinds this binary may no longer know.
type Kind string

// Core is everything a protocol engine must implement. It is deliberately three
// methods; anything a core might not be able to do is an optional capability
// interface in caps.go, discovered once by Bind.
type Core interface {
	// Describe returns static facts about this core, including the capabilities
	// it claims. coretest asserts the claims match the interfaces implemented.
	Describe() Descriptor

	// Kinds are the protocols this core serves. One core may serve several —
	// accel-ppp would answer l2tp, pptp and sstp.
	Kinds() []Kind

	// Preflight reports whether this core can run here: binary present, kernel
	// module loadable, version supported. A failure disables the core, it does
	// not stop the panel.
	Preflight(ctx context.Context) error
}

// Descriptor is the static, serialisable description of a core. It crosses the
// API boundary to the UI, so it must stay free of runtime handles.
type Descriptor struct {
	ID Kind `json:"id"`
	// TitleKey is an i18n key, not a label. Keys live in Go so the frontend's
	// dead-key test counts them as referenced.
	TitleKey string       `json:"titleKey"`
	Caps     Capabilities `json:"caps"`
}

// Capabilities is what a core claims it can do. Every field is a *bool because
// nil means UNKNOWN, which is a normal state: a master talking to a node one
// release behind cannot distinguish "cannot" from "was never asked".
type Capabilities struct {
	UserHotAdd    *bool `json:"userHotAdd"`
	PerUserStats  *bool `json:"perUserStats"`
	QuotaPushdown *bool `json:"quotaPushdown"`
	OnlineUsers   *bool `json:"onlineUsers"`
	ShareLink     *bool `json:"shareLink"`
}

// Known reports whether a capability was answered at all. Callers must treat a
// nil capability as "ask the node", never as false.
func Known(v *bool) bool { return v != nil && *v }

// Yes and No build capability values; a core must set every field it knows.
func Yes() *bool { t := true; return &t }
func No() *bool  { f := false; return &f }

// Instance is the desired state of one inbound under one core. It is what a
// core reconciles towards, and it carries no DB or panel types.
type Instance struct {
	ID       int
	Kind     Kind
	Tag      string
	Listen   string
	Port     int
	Enable   bool
	Settings string
	Users    []User
}

// User is one client as a core sees it. Credentials are per-core and opaque
// here: an OpenVPN cert, an ocserv password hash, a WireGuard public key.
type User struct {
	Email           string
	Enable          bool
	QuotaBytes      int64
	ExpiryUnixMilli int64
	Credentials     map[string]string
}

// Action is how a change must be applied. Restart drops live connections, so a
// core that can hot-apply must say so rather than defaulting to Restart.
type Action int

const (
	ActionNoop Action = iota
	ActionHotApply
	ActionRestart
)

func (a Action) String() string {
	switch a {
	case ActionNoop:
		return "noop"
	case ActionHotApply:
		return "hot-apply"
	case ActionRestart:
		return "restart"
	default:
		return "unknown"
	}
}
