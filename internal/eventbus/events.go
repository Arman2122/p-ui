package eventbus

import "time"

// EventType identifies the kind of event flowing through the bus.
type EventType string

const (
	// Outbound health (observatory-driven)
	EventOutboundDown EventType = "outbound.down"
	EventOutboundUp   EventType = "outbound.up"

	/*
		A core's daemon died on its own.

		Core-generic and instance-aware, because the two shapes both ship: Xray is
		one process per host, mtg is one per inbound. EventXrayCrash predates the
		multi-core work and is kept because the Xray path is separately tested;
		everything a core publishes goes through EventCoreCrash, which carries who
		died so a notification can say so.
	*/
	EventCoreCrash EventType = "core.crash"
	// EventCoreUp is the same daemon running and answering again after a crash
	// this bus reported, so an alert has its all-clear; the payload is the crash's.
	EventCoreUp EventType = "core.up"

	// Xray core (local)
	EventXrayCrash EventType = "xray.crash"

	// Node health (heartbeat-driven)
	EventNodeDown EventType = "node.down"
	EventNodeUp   EventType = "node.up"

	// System health
	EventCPUHigh    EventType = "cpu.high"
	EventMemoryHigh EventType = "memory.high"

	// Security
	EventLoginAttempt EventType = "login.attempt"
	// EventCoreAuthFailure is a core refusing a client login (SSH); kept apart
	// from login.attempt, which is the panel's own admin login and locks accounts.
	EventCoreAuthFailure EventType = "core.auth.failure"

	// EventClientDeviceLimit is a device over a client's cap and what was done
	// about it: cut by its core, dropped on a firewall, or reported alone.
	EventClientDeviceLimit  EventType = "client.device_limit"
	EventClientPlanDepleted EventType = "client.plan_depleted"
)

// ClientDeviceLimitData carries one device over its cap. Action is "cut", "ban"
// or "reported"; Where is "" for this host or the name of the node it was dropped on.
type ClientDeviceLimitData struct {
	Email   string
	IP      string
	Limit   int
	Devices int
	Action  string
	Where   string
	Until   int64
}

// Event is the unit of information flowing through the bus.
type Event struct {
	Type      EventType
	Source    string    // outbound tag, node name, client email, IP, etc.
	Data      any       // event-specific payload, may be nil
	Timestamp time.Time // when the event was detected
}

// CoreCrashData names which core's daemon died, and which of its instances when
// the core runs one process per inbound.
type CoreCrashData struct {
	CoreID     string
	InstanceID int
	Err        string
}

// CoreAuthFailureData carries one refused client login.
type CoreAuthFailureData struct {
	CoreID     string
	InstanceID int
	Username   string
	IP         string
	Reason     string
}

// OutboundHealthData carries observatory details for outbound events.
type OutboundHealthData struct {
	Delay int64  // last measured delay in ms, 0 if unknown
	Error string // last error if probe failed, empty if up
}

// NodeHealthData carries heartbeat details for node events.
type NodeHealthData struct {
	NodeId    int
	LatencyMs int
	CpuPct    float64
	MemPct    float64
	XrayState string // "running", "stopped", etc.
	XrayError string
}

// LoginEventData carries login attempt details.
type LoginEventData struct {
	Username string
	IP       string
	Time     string
	Status   string // "success" or "fail"
	Reason   string
}

// SystemMetricData carries raw system metric values for threshold-based events.
type SystemMetricData struct {
	Percent   float64 // current usage percentage
	Threshold int     // configured threshold
}
