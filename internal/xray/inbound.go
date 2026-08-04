package xray

import "github.com/Arman2122/p-ui/internal/core"

// InboundConfig moved to internal/core alongside ClientTraffic, for the same
// reason: the model layer generates one and must not import a core to do it.
type InboundConfig = core.InboundConfig
