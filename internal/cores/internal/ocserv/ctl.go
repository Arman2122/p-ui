package ocserv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"
)

/*
The verbs this core sends the daemon end sessions: a user's, or one device's.
Each is half of an enforcement, never the whole one -- ocserv keeps the session
cookie valid, so a client disconnected while the panel would still admit it
simply reconnects without authenticating. The caller refuses the login first.

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
	// "could not disconnect user 'x'" is what a user with no session gets, and
	// occtl exits non-zero saying it — done, not an error to end.
	if strings.Contains(answer, "could not disconnect") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ocserv: disconnect %s: %w: %s", username, err, answer)
	}
	if strings.Contains(answer, "unknown command") {
		return fmt.Errorf("ocserv: occtl refused the disconnect: %s", answer)
	}
	return nil
}

/*
disconnectSource ends only the sessions a user holds from ONE source address, so
the device over a cap is dropped and the ones inside it stay connected.
`disconnect user` would take them all — occtl's own `-j show users` names each
session's id and remote address, and only the ones from source are disconnected
by id. Returns how many it ended.
*/
func occtlDisconnectSource(ctx context.Context, occtl, socket, username string, source netip.Addr) (int, error) {
	if strings.ContainsAny(username, " \t\r\n\"'") {
		return 0, fmt.Errorf("ocserv: refusing to disconnect %q", username)
	}
	rows, err := listUsers(ctx, occtl, socket)
	if err != nil {
		if errors.Is(err, errNoDaemon) {
			return 0, nil
		}
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cut := 0
	for _, r := range rows {
		if r.Username != username || r.ID == "" {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(r.RemoteIP))
		if err != nil || addr != source {
			continue
		}
		if err := exec.CommandContext(ctx, occtl, "-s", socket, "disconnect", "id", r.ID.String()).Run(); err != nil {
			return cut, fmt.Errorf("ocserv: disconnect id %s: %w", r.ID.String(), err)
		}
		cut++
	}
	return cut, nil
}

// userRow is one session as `occtl -j show users` lists it. The keys are the
// daemon's own; only the ones the panel reads are declared.
type userRow struct {
	ID       json.Number `json:"ID"`
	Username string      `json:"Username"`
	RemoteIP string      `json:"Remote IP"`
	Device   string      `json:"Device"`
	IPv4     string      `json:"IPv4"`
	IPv6     string      `json:"IPv6"`
}

// errNoDaemon: no control socket means no daemon, and no daemon lists no session.
var errNoDaemon = errors.New("ocserv: no control socket")

// The occtl-backed answers; the daemon-less conformance rig swaps them.
var (
	listUsers        = occtlUsers
	disconnectSource = occtlDisconnectSource
	daemonAnswers    = occtlStatus
)

// listUsers asks the daemon for its live sessions: the one source that knows
// which of its transient devices carries whom.
func occtlUsers(ctx context.Context, occtl, socket string) ([]userRow, error) {
	if _, err := os.Stat(socket); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errNoDaemon
		}
		return nil, fmt.Errorf("ocserv: control socket: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, occtl, "-s", socket, "-j", "show", "users").Output()
	if err != nil {
		return nil, fmt.Errorf("ocserv: show users: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var rows []userRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("ocserv: parse users: %w", err)
	}
	return rows, nil
}

// daemonAnswers asks the main process for its status through occtl, which is
// what "ready" means for a daemon whose listeners the panel cannot see.
func occtlStatus(ctx context.Context, occtl, socket string) error {
	out, err := exec.CommandContext(ctx, occtl, "-s", socket, "show", "status").CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
