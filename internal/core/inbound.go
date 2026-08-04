package core

import (
	"bytes"

	"github.com/Arman2122/p-ui/internal/util/json_util"
)

// InboundConfig is the generated, engine-facing shape of one inbound. The field
// set is deliberately protocol-neutral — a core reads Protocol/Port/Settings and
// renders whatever its daemon wants; Xray happens to consume it as JSON directly.
type InboundConfig struct {
	Listen         json_util.RawMessage `json:"listen"` // listen cannot be an empty string
	Port           int                  `json:"port"`
	Protocol       string               `json:"protocol"`
	Settings       json_util.RawMessage `json:"settings"`
	StreamSettings json_util.RawMessage `json:"streamSettings,omitempty"`
	Tag            string               `json:"tag"`
	Sniffing       json_util.RawMessage `json:"sniffing,omitempty"`
}

// Equals compares two InboundConfig instances for deep equality.
func (c *InboundConfig) Equals(other *InboundConfig) bool {
	if !bytes.Equal(c.Listen, other.Listen) {
		return false
	}
	if c.Port != other.Port {
		return false
	}
	if c.Protocol != other.Protocol {
		return false
	}
	if !bytes.Equal(c.Settings, other.Settings) {
		return false
	}
	if !bytes.Equal(c.StreamSettings, other.StreamSettings) {
		return false
	}
	if c.Tag != other.Tag {
		return false
	}
	if !bytes.Equal(c.Sniffing, other.Sniffing) {
		return false
	}
	return true
}
