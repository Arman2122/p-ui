package ocserv

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"
)

/*
The one verb this core sends the daemon: end a user's sessions. It is half
of an enforcement, never the whole one -- ocserv keeps the session cookie
valid, so a client disconnected while the panel would still admit it simply
reconnects without authenticating. The caller refuses the login first.

occtl 1.2's `terminate`, which invalidates the cookie too, does not exist:
the packaged binary offers `disconnect` alone.
*/
func disconnectUser(ctx context.Context, occtl, socket, username string) error {
	if strings.ContainsAny(username, " \t\r\n\"'") {
		return fmt.Errorf("ocserv: refusing to disconnect %q", username)
	}
	if _, err := os.Stat(socket); err != nil {
		// No socket means no daemon, and no daemon holds no session to end: a
		// user removed while the process is down or still starting is done.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("ocserv: control socket: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, occtl, "-s", socket, "disconnect", "user", username).CombinedOutput()
	answer := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("ocserv: disconnect %s: %w: %s", username, err, answer)
	}
	// "user 'x' was disconnected" or "could not disconnect user 'x'", which is
	// what a user with no session gets and is not an error to end.
	if strings.Contains(answer, "unknown command") {
		return fmt.Errorf("ocserv: occtl refused the disconnect: %s", answer)
	}
	return nil
}
