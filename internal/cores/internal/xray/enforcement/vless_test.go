package enforcement

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	xuuid "github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	vencoding "github.com/xtls/xray-core/proxy/vless/encoding"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestRealVLESSQuotaTCPAndUDP(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			var target string
			if network == "tcp" {
				target = echoServer(t)
			} else {
				c, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				target = c.LocalAddr().String()
				done := make(chan struct{})
				go func() {
					defer close(done)
					data := make([]byte, 65535)
					for {
						n, remote, err := c.ReadFrom(data)
						if err != nil {
							return
						}
						_, _ = c.WriteTo(data[:n], remote)
					}
				}()
				t.Cleanup(func() { _ = c.Close(); <-done })
			}
			a, err := limits.New(limits.Policy{
				Term: "term-a", Revision: 1, Enabled: true, CreditCeiling: 4096,
				Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
			})
			if err != nil {
				t.Fatal(err)
			}
			const userID = "55834271-a5db-44c0-8a74-f988601d427b"
			port := freePort(t)
			startConfiguredCore(t, func(id string) (*limits.Account, bool) { return a, id == "client-c" }, []map[string]any{{
				"tag": "vless", "listen": "127.0.0.1", "port": port, "protocol": "vless",
				"settings": map[string]any{"decryption": "none", "clients": []map[string]string{{"id": userID, "email": "client-c"}}},
			}})
			id, err := xuuid.ParseString(userID)
			if err != nil {
				t.Fatal(err)
			}
			host, targetPort, err := net.SplitHostPort(target)
			if err != nil {
				t.Fatal(err)
			}
			p, err := strconv.Atoi(targetPort)
			if err != nil {
				t.Fatal(err)
			}
			command := protocol.RequestCommandTCP
			if network == "udp" {
				command = protocol.RequestCommandUDP
			}
			request := &protocol.RequestHeader{
				Version: vencoding.Version, Command: command, Address: xnet.ParseAddress(host), Port: xnet.Port(p),
				User: &protocol.MemoryUser{Account: &vless.MemoryAccount{ID: protocol.NewID(id)}},
			}
			for range 2 {
				c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.Close() })
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				if err := vencoding.EncodeRequestHeader(c, request, &vencoding.Addons{}); err != nil {
					t.Fatal(err)
				}
				payload := bytes.Repeat([]byte("v"), 1024)
				if network == "udp" {
					header := binary.BigEndian.AppendUint16(nil, uint16(len(payload)))
					if _, err := c.Write(header); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := c.Write(payload); err != nil {
					t.Fatal(err)
				}
				if _, err := vencoding.DecodeResponseHeader(c, request); err != nil {
					t.Fatal(err)
				}
				if network == "udp" {
					var header [2]byte
					if _, err := io.ReadFull(c, header[:]); err != nil {
						t.Fatal(err)
					}
					if n := binary.BigEndian.Uint16(header[:]); n != 1024 {
						t.Fatalf("packet length=%d", n)
					}
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(c, got); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatal("VLESS echo differs")
				}
				_ = c.Close()
			}
			if s := a.Snapshot(); s.AdmittedUpload != 2048 || s.AdmittedDownload != 2048 {
				t.Fatalf("VLESS usage=%+v", s)
			}
		})
	}
}
