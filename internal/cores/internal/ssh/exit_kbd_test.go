package ssh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
)

// kbdOnlySSHD is a remote that offers keyboard-interactive alone, the way a
// PAM-backed sshd does by default, and forwards direct-tcpip channels.
func kbdOnlySSHD(t *testing.T, password string) (host string, port int, fingerprint string) {
	t.Helper()
	signer, err := gossh.ParsePrivateKey([]byte(testHostKeyPEM(t)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &gossh.ServerConfig{KeyboardInteractiveCallback: func(meta gossh.ConnMetadata, ask gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
		answers, err := ask(meta.User(), "", []string{"Password: "}, []bool{false})
		if err != nil || len(answers) != 1 || answers[0] != password {
			return nil, errors.New("denied")
		}
		return &gossh.Permissions{}, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := gossh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go gossh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						_ = nc.Reject(gossh.UnknownChannelType, "")
						continue
					}
					var req struct {
						Host     string
						Port     uint32
						Orig     string
						OrigPort uint32
					}
					if gossh.Unmarshal(nc.ExtraData(), &req) != nil {
						_ = nc.Reject(gossh.ConnectionFailed, "")
						continue
					}
					dst, err := net.Dial("tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
					if err != nil {
						_ = nc.Reject(gossh.ConnectionFailed, "")
						continue
					}
					ch, chReqs, err := nc.Accept()
					if err != nil {
						_ = dst.Close()
						continue
					}
					go gossh.DiscardRequests(chReqs)
					go func() { _, _ = io.Copy(dst, ch); _ = dst.Close() }()
					go func() { _, _ = io.Copy(ch, dst); _ = ch.Close() }()
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, gossh.FingerprintSHA256(signer.PublicKey())
}

// A remote offering keyboard-interactive alone refused the exit's one password
// method with "no supported methods remain"; the same password answers it now.
func TestTheExitAnswersAKeyboardInteractiveOnlyServer(t *testing.T) {
	echo := echoListener(t)
	host, port, fingerprint := kbdOnlySSHD(t, "secret-0")
	c := New(Deps{})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	settings, _ := json.Marshal(ExitSettings{Host: host, Port: port, Username: "user0", Password: "secret-0", HostKeyFingerprint: fingerprint})
	exit := core.Exit{ID: 5, Kind: Kind, Enable: true, Settings: string(settings)}
	if err := c.ReconcileExits(context.Background(), []core.Exit{exit}); err != nil {
		t.Fatalf("ReconcileExits: %v", err)
	}
	conn := waitSocks(t, core.ExitSocksPortFor(exit.ID), echo.Addr(), true)
	defer conn.Close()
	if _, err := conn.Write([]byte("kbd")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 3)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "kbd" {
		t.Fatalf("read back %q, %v", got, err)
	}
}
