package link

import "fmt"

// MigrateQuicUdpHop moves finalmask.quicParams.udpHop, which xray-core v26.9.9
// stopped reading, onto the "udphop" UDP mask that replaced it; it reports a change.
func MigrateQuicUdpHop(finalmask map[string]any) bool {
	quicParams, _ := finalmask["quicParams"].(map[string]any)
	legacy, present := quicParams["udpHop"]
	if !present {
		return false
	}
	delete(quicParams, "udpHop")
	if len(quicParams) == 0 {
		delete(finalmask, "quicParams")
	}
	settings := map[string]any{"mode": "intervalLocal,intervalRemote"}
	if hop, ok := legacy.(map[string]any); ok {
		if ports := rangeString(hop["ports"]); ports != "" {
			settings["remotePorts"] = ports
		}
		if interval := rangeString(hop["interval"]); interval != "" {
			settings["interval"] = interval
		}
	}
	udp, _ := finalmask["udp"].([]any)
	finalmask["udp"] = append(udp, map[string]any{"type": "udphop", "settings": settings})
	return true
}

// rangeString reads a "from-to" field the form once stored as a bare number.
func rangeString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%d", int64(value))
	default:
		return ""
	}
}
