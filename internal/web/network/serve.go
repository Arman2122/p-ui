package network

import (
	"errors"
	"net"
	"net/http"

	"github.com/Arman2122/p-ui/internal/logger"
)

// ServeHTTP reports listener failures while keeping normal shutdown silent.
func ServeHTTP(server *http.Server, listener net.Listener, name string) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(name, " stopped unexpectedly: ", err)
	}
}
