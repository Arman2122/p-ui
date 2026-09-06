package service

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// inboundCipherMethod reads settings.method, which the client UI needs to mint
// a PSK of the right length for a Shadowsocks 2022 cipher; "" when there is none.
func inboundCipherMethod(settings string) string {
	if settings == "" {
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

/*
checkFallbacksSurviveEdit refuses an edit that moves an inbound off the shape
its own fallbacks need — turning VLESS encryption on is the reachable one, since
the form offers "generate VLESS auth" and the Fallbacks card in the same modal.

Refused rather than dropped: the rows stay attached, the child inbounds keep
listening, and nothing anywhere says why they stopped receiving traffic.
*/
func (s *InboundService) checkFallbacksSurviveEdit(inbound *model.Inbound) error {
	if inbound == nil || inbound.Id == 0 || inboundCanHostFallbacks(inbound) {
		return nil
	}
	rows, err := s.fallbackService.GetByMaster(inbound.Id)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return common.NewError("this inbound has", len(rows), "fallback(s):", errFallbackHostShape+
		". Remove them before making this change")
}
