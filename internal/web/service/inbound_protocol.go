package service

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// inboundShadowsocksMethod extracts settings.method for Shadowsocks inbounds so
// the client UI can generate a valid PSK (base64 of the method's key length)
// for Shadowsocks 2022 ciphers. Returns "" for non-Shadowsocks inbounds.
func inboundShadowsocksMethod(protocol, settings string) string {
	if protocol != string(model.Shadowsocks) || settings == "" {
		return ""
	}
	var s struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		return ""
	}
	return s.Method
}

// inboundCanEnableTlsFlow reports whether XTLS Vision is valid for this inbound.
// The rule lives in internal/core and is shared with internal/sub and the
// frontend, which each used to carry their own copy of it.
func inboundCanEnableTlsFlow(protocol, streamSettings, settings string) bool {
	return core.Can(core.CapTLSFlow, core.FactsFromJSON(protocol, settings, streamSettings))
}

// inboundCanHostFallbacks gates the settings.fallbacks injection. Deliberately
// stricter than Vision: fallbacks are raw-TCP only, never XHTTP.
func inboundCanHostFallbacks(ib *model.Inbound) bool {
	if ib == nil {
		return false
	}
	return core.Can(core.CapFallbacks, core.FactsFromJSON(string(ib.Protocol), ib.Settings, ib.StreamSettings))
}
