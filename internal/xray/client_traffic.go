package xray

import "github.com/Arman2122/p-ui/internal/core"

// ClientTraffic moved to internal/core: it is the cross-core usage row, and
// keeping it here made the model layer import this package. Alias, not a copy —
// both names must stay the same type while call sites migrate.
type ClientTraffic = core.ClientTraffic
