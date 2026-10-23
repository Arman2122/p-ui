package ssh

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestAuthenticatedSSHDemandNeverAuthorizesTheDeniedAttempt(t *testing.T) {
	for _, initial := range []string{"missing", "depleted"} {
		t.Run(initial, func(t *testing.T) {
			user := userN(0)
			var current atomic.Pointer[limits.Account]
			policy := limits.Policy{Term: "ssh-demand", Revision: 1, Enabled: true, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
			if initial == "depleted" {
				depleted, err := limits.New(policy)
				if err != nil {
					t.Fatal(err)
				}
				current.Store(depleted)
			}
			key := testHostKeyPEM(t)
			signer, err := gossh.ParsePrivateKey([]byte(key))
			if err != nil {
				t.Fatal(err)
			}
			events := make(chan [2]string, 8)
			var destinations atomic.Int32
			engine := New(Deps{
				LoadHostKey: func() (string, error) { return key, nil },
				Governor: func(client string) (*limits.Account, bool) {
					account := current.Load()
					return account, client == user.Email && account != nil
				},
				Demand: func(_ context.Context, client, inbound string) error {
					events <- [2]string{client, inbound}
					return nil
				},
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					destinations.Add(1)
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
				},
			})
			inbound := core.Instance{ID: 42, Kind: Kind, Tag: "node-owned-ssh-target", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{}`, Users: []core.User{user}}
			if err := engine.Reconcile(t.Context(), []core.Instance{inbound}); err != nil {
				t.Fatal(err)
			}
			defer engine.StopAll(context.Background())
			connect := func(username, password string) (*gossh.Client, error) {
				raw, err := net.DialTimeout("tcp", net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.Port)), time.Second)
				if err != nil {
					return nil, err
				}
				_ = raw.SetDeadline(time.Now().Add(2 * time.Second))
				conn, channels, requests, err := gossh.NewClientConn(raw, raw.RemoteAddr().String(), &gossh.ClientConfig{User: username, Auth: []gossh.AuthMethod{gossh.Password(password)}, HostKeyCallback: gossh.FixedHostKey(signer.PublicKey()), HostKeyAlgorithms: []string{signer.PublicKey().Type()}})
				if err != nil {
					_ = raw.Close()
					return nil, err
				}
				return gossh.NewClient(conn, channels, requests), nil
			}
			username, password := core.CredString(user.Credentials, core.CredUsername), core.CredString(user.Credentials, core.CredPassword)
			for _, credentials := range [][2]string{{username, "wrong-password"}, {"unknown-user", password}} {
				client, err := connect(credentials[0], credentials[1])
				if err == nil {
					_ = client.Close()
					t.Fatal("demand receipt authorized an unfunded SSH login")
				}
			}
			for range throttleFailures + 2 {
				client, err := connect(username, password)
				if err == nil {
					_ = client.Close()
					t.Fatal("demand receipt authorized an unfunded SSH login")
				}
			}
			if len(events) != throttleFailures+2 {
				t.Fatal("SSH demand was not restricted to the authenticated client and target inbound")
			}
			for range throttleFailures + 2 {
				if event := <-events; event != [2]string{user.Email, inbound.Tag} {
					t.Fatalf("wrong authenticated retry identity: %v", event)
				}
			}
			if destinations.Load() != 0 {
				t.Fatal("denied SSH demand contacted a destination")
			}
			policy.CreditCeiling = 4096
			funded, err := limits.New(policy)
			if err != nil {
				t.Fatal(err)
			}
			current.Store(funded)
			client, err := connect(username, password)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			echo := echoListener(t)
			forward, err := client.Dial("tcp", echo.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer forward.Close()
			if _, err := forward.Write([]byte("verified")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 8)
			if _, err := io.ReadFull(forward, got); err != nil || string(got) != "verified" {
				t.Fatalf("funded next attempt failed: %q %v", got, err)
			}
			if len(events) != 0 || destinations.Load() != 1 {
				t.Fatal("funded SSH login emitted a duplicate demand")
			}
			for range throttleFailures {
				if wrong, err := connect(username, "incorrect-password"); err == nil {
					_ = wrong.Close()
					t.Fatal("wrong password was accepted")
				}
			}
			if banned, err := connect(username, password); err == nil {
				_ = banned.Close()
				t.Fatal("wrong-password threshold no longer bans the source")
			}
			if len(events) != 0 {
				t.Fatal("wrong-password retries produced authenticated demand")
			}
		})
	}
}
