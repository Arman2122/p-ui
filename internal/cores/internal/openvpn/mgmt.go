package openvpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

/*
The management socket admits one client at a time, and this core is it for
two verbs: `kill <common name>`, which ends every session of a user the panel
has disabled, depleted or removed, and `status 3` followed by `kill <host:port>`,
which ends one device's sessions and no other's. Traffic and sessions come from
RADIUS, so the socket is opened per command and closed at once, never held; a
held socket would make the panel a hard dependency of the daemon.
*/

// management opens the socket and reads past its greeting. A nil conn with a nil
// error is no daemon, which holds no session: the caller has nothing to end.
func management(ctx context.Context, sock string) (net.Conn, *bufio.Reader, error) {
	d := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("openvpn: management: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("openvpn: management greeting: %w", err)
	}
	return conn, reader, nil
}

// kill sends one kill verb and reads its one-line answer. A target with no
// session ("not found") is not an error to end; any other ERROR is.
func kill(conn net.Conn, reader *bufio.Reader, target string) (killed bool, err error) {
	if _, err := fmt.Fprintf(conn, "kill %s\n", target); err != nil {
		return false, err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("openvpn: management kill: %w", err)
	}
	if strings.HasPrefix(line, "ERROR:") {
		if strings.Contains(line, "not found") {
			return false, nil
		}
		return false, fmt.Errorf("openvpn: %s", strings.TrimSpace(line))
	}
	return true, nil
}

// killSessions ends every session of one common name; the verb the panel uses
// when the user itself is gone, disabled or depleted.
func killSessions(ctx context.Context, sock, commonName string) error {
	if strings.ContainsAny(commonName, "\r\n\"") {
		return fmt.Errorf("openvpn: refusing to kill %q", commonName)
	}
	conn, reader, err := management(ctx, sock)
	if err != nil || conn == nil {
		return err
	}
	defer conn.Close()
	_, err = kill(conn, reader, commonName)
	return err
}

/*
cutSource ends only the sessions a common name holds from ONE source address, so
the device over a cap is dropped and the ones inside it stay connected. It reads
`status 3` for the real address (host:port) of each of the common name's sessions
— kill by common name alone would take them all — and kills each whose host is
the source. The common name is only compared here, never sent. Returns how many
it killed.
*/
func cutSource(ctx context.Context, sock, commonName string, source netip.Addr) (int, error) {
	conn, reader, err := management(ctx, sock)
	if err != nil || conn == nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "status 3\n"); err != nil {
		return 0, err
	}
	var addresses []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("openvpn: management status: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "END" {
			break
		}
		// CLIENT_LIST<TAB>common name<TAB>real address<TAB>...; the real address
		// is host:port, and one common name can hold several of them.
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || fields[0] != "CLIENT_LIST" || fields[1] != commonName {
			continue
		}
		host, _, err := net.SplitHostPort(fields[2])
		if err != nil {
			continue
		}
		if addr, err := netip.ParseAddr(host); err == nil && addr == source {
			addresses = append(addresses, fields[2])
		}
	}
	cut := 0
	for _, address := range addresses {
		killed, err := kill(conn, reader, address)
		if err != nil {
			return cut, err
		}
		if killed {
			cut++
		}
	}
	return cut, nil
}
