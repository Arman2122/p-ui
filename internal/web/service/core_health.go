package service

import (
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
)

type coreTrafficState struct {
	lastOK  time.Time
	lastErr string
}

var (
	coreTrafficMu sync.Mutex
	coreTraffic   = map[string]coreTrafficState{}
)

// NoteCoreTraffic records one poll's outcome for a core and logs the
// transitions, not every tick: a core that stopped reporting used to be silent.
func NoteCoreTraffic(id string, err error) {
	coreTrafficMu.Lock()
	defer coreTrafficMu.Unlock()
	state := coreTraffic[id]
	if err == nil {
		if state.lastErr != "" {
			logger.Info("core", id, "traffic collection recovered")
		}
		state.lastOK, state.lastErr = time.Now(), ""
	} else {
		if state.lastErr == "" {
			logger.Warning("core", id, "traffic collection failing; its clients are not being billed:", err)
		}
		state.lastErr = err.Error()
	}
	coreTraffic[id] = state
}

// CoreTrafficHealth answers when a core last reported traffic and what is
// wrong now; a zero time means it has not reported since the panel started.
func CoreTrafficHealth(id string) (lastOK time.Time, lastErr string) {
	coreTrafficMu.Lock()
	defer coreTrafficMu.Unlock()
	state := coreTraffic[id]
	return state.lastOK, state.lastErr
}
