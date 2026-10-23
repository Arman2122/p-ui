package ssh

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestGovernorSharesCreditAcrossSSHInboundsAndUDP(t *testing.T) {
	user := userN(0)
	account, err := limits.New(limits.Policy{
		Term: "ssh-term", Revision: 1, Enabled: true, CreditCeiling: 3072,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := testHostKeyPEM(t)
	signer, err := gossh.ParsePrivateKey([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		LoadHostKey: func() (string, error) { return key, nil }, Dial: (&net.Dialer{Timeout: time.Second}).DialContext,
		Governor: func(email string) (*limits.Account, bool) { return account, email == user.Email },
	})
	inbounds := []core.Instance{
		{ID: 1, Kind: Kind, Tag: "one", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{"udpgw":true}`, Users: []core.User{user}},
		{ID: 2, Kind: Kind, Tag: "two", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{"udpgw":true}`, Users: []core.User{user}},
	}
	if err := engine.Reconcile(t.Context(), inbounds); err != nil {
		t.Fatal(err)
	}
	defer engine.StopAll(context.Background())
	var clients []*gossh.Client
	for _, inbound := range inbounds {
		client, err := gossh.Dial("tcp", net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.Port)), &gossh.ClientConfig{
			User: core.CredString(user.Credentials, core.CredUsername), Auth: []gossh.AuthMethod{gossh.Password(core.CredString(user.Credentials, core.CredPassword))},
			HostKeyCallback: gossh.FixedHostKey(signer.PublicKey()), HostKeyAlgorithms: []string{signer.PublicKey().Type()}, Timeout: time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		clients = append(clients, client)
	}
	echo := echoListener(t)
	forward, err := clients[0].Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	data := bytes.Repeat([]byte("a"), 512)
	if _, err := forward.Write(data); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 512)
	if _, err := io.ReadFull(forward, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("TCP payload changed")
	}
	udp := openUDPGW(t, clients[1], udpgwDefaultPort)
	defer udp.Close()
	destination := udpEcho(t)
	if _, err := udp.Write(udpgwFrame(0, 1, destination, data)); err != nil {
		t.Fatal(err)
	}
	_, _, _, got = readUDPGWFrame(t, udp)
	if !bytes.Equal(got, data) {
		t.Fatal("UDP payload changed")
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 1024 || snapshot.AdmittedDownload != 1024 {
		t.Fatalf("combined admissions=%+v", snapshot)
	}
	if _, err := forward.Write(bytes.Repeat([]byte("x"), 2048)); err != nil {
		t.Fatal(err)
	}
	got, err = readWithin(forward, time.Second)
	if len(got) != 0 || err == nil {
		t.Fatalf("quota boundary leaked data: %d,%v", len(got), err)
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload+snapshot.AdmittedDownload != 3072 {
		t.Fatalf("combined quota=%+v", snapshot)
	}
}
