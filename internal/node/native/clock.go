package native

import (
	"time"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (c *Controller) SetClock(clock limits.Clock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock = clock
	if plane, ok := c.plane.(interface{ SetClock(limits.Clock) }); ok {
		plane.SetClock(clock)
	}
}

func authorizationTime(clock limits.Clock) (time.Time, error) {
	if clock != nil {
		return clock.Now()
	}
	return time.Now(), nil
}
