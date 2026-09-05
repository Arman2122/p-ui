package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/cores"
)

// SSHHostKey is what an SSH server presented, for the operator to pin.
type SSHHostKey struct {
	Fingerprint string `json:"fingerprint" example:"SHA256:slAhPUsjKT/+eP4DHWVimm+a5yTcX8Sw13dOOHWn/L4"`
	KeyType     string `json:"keyType" example:"ssh-ed25519"`
}

var errHostKeySeen = errors.New("host key captured")

/*
ScanSSHHostKey performs the key exchange with host:port and reports the key
the server presented, without authenticating: the exchange ends the moment the
key is known. The operator still decides whether to pin it; this only spares
them the keyscan and keygen incantation, and a typo in either.
*/
func ScanSSHHostKey(ctx context.Context, host string, port int) (SSHHostKey, error) {
	if host == "" || port < 1 || port > 65535 {
		return SSHHostKey{}, errors.New("ssh host key: a host and a port are required")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return SSHHostKey{}, fmt.Errorf("ssh host key: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var seen SSHHostKey
	cfg := &gossh.ClientConfig{
		User: "hostkey-scan",
		// The exit's own preference, so the key reported here is the key it pins.
		HostKeyAlgorithms: cores.SSHHostKeyAlgorithms(),
		HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
			seen = SSHHostKey{Fingerprint: gossh.FingerprintSHA256(key), KeyType: key.Type()}
			return errHostKeySeen
		},
		Timeout: 10 * time.Second,
	}
	_, _, _, err = gossh.NewClientConn(conn, net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if seen.Fingerprint != "" {
		return seen, nil
	}
	return SSHHostKey{}, fmt.Errorf("ssh host key: %w", err)
}
