package openvpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"syscall"
	"time"
)

/*
The management socket admits one client at a time, and this core is it for
exactly one verb: `kill <common name>`, which ends every session of a user
the panel has disabled, depleted or removed. Traffic and sessions come from
RADIUS, so the socket is opened per command and closed at once, never held;
a held socket would make the panel a hard dependency of the daemon.
*/
func killSessions(ctx context.Context, sock, commonName string) error {
	if strings.ContainsAny(commonName, "\r\n\"") {
		return fmt.Errorf("openvpn: refusing to kill %q", commonName)
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		// No socket means no daemon, and no daemon holds no session to end: a
		// user removed while the process is down or still starting is done.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return fmt.Errorf("openvpn: management: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	// The greeting line first; then the verb, then its one-line answer.
	if _, err := reader.ReadString('\n'); err != nil {
		return fmt.Errorf("openvpn: management greeting: %w", err)
	}
	if _, err := fmt.Fprintf(conn, "kill %s\n", commonName); err != nil {
		return err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("openvpn: management kill: %w", err)
	}
	// "SUCCESS: common name 'x' found, 1 client(s) killed" or "ERROR: common
	// name 'x' not found": a user with no session is not an error to end.
	if strings.HasPrefix(line, "ERROR:") && !strings.Contains(line, "not found") {
		return fmt.Errorf("openvpn: %s", strings.TrimSpace(line))
	}
	return nil
}
