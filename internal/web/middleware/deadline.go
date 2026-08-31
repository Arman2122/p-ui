package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/logger"
)

/*
LongRequest lifts the server's write deadline for one route.

http.Server.WriteTimeout is 30s for the whole panel — a Slowloris guard for the
general case, and the wrong answer for the few admin routes whose work is a
download, a pg_dump or a fan-out of network probes. Those were cut mid-response:
the operator saw a dead connection while the work ran on to completion, and the
obvious reaction — retry — started a second one.

The budget is not the operation's. Each of those has a timeout of its own and
finishes long before this; this only has to outlast the largest of them, which
is updateGeofile fetching six files under a five-minute client each. F284.
*/
const longRequestBudget = 30 * time.Minute

func LongRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		deadline := time.Now().Add(longRequestBudget)
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(deadline); err != nil {
			logger.Debug("write deadline stays at the server default for", c.Request.URL.Path, err)
		}
		c.Next()
	}
}
