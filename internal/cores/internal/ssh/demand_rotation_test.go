package ssh

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestAuthenticatedSSHOldProofCannotAdoptRotatedCredentialGeneration(t *testing.T) {
	user := userN(0)
	user.Email = uuid.NewString()
	oldPassword := core.CredString(user.Credentials, core.CredPassword)
	key := testHostKeyPEM(t)
	signer, err := gossh.ParsePrivateKey([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	inbound := core.Instance{ID: 42, Kind: Kind, Tag: "immutable-ssh-proof", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{}`, Users: []core.User{user}}
	descriptor := admission.Descriptor{ClientID: user.Email, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: inbound.Tag}
	broker := admission.New("ssh", func() (time.Time, error) { return time.Now(), nil })
	if err := broker.Replace(1, []admission.Descriptor{descriptor}); err != nil {
		t.Fatal(err)
	}
	oldProof, err := broker.Bind([]admission.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = broker.Replace(0, nil)
	}()
	create := func(handler admission.Handler) *Core {
		t.Helper()
		engine := New(Deps{LoadHostKey: func() (string, error) { return key, nil }, Governor: func(string) (*limits.Account, bool) { return nil, false }, Demand: handler, DemandContext: t.Context()})
		if err := engine.Reconcile(t.Context(), []core.Instance{inbound}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = engine.StopAll(context.Background()) })
		return engine
	}
	engine := create(func(ctx context.Context, client, tag string) error {
		close(entered)
		<-release
		return oldProof(ctx, client, tag)
	})
	connect := func(password string) error {
		raw, err := net.DialTimeout("tcp", net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.Port)), time.Second)
		if err != nil {
			return err
		}
		defer raw.Close()
		_ = raw.SetDeadline(time.Now().Add(2 * time.Second))
		connection, _, _, err := gossh.NewClientConn(raw, raw.RemoteAddr().String(), &gossh.ClientConfig{User: core.CredString(user.Credentials, core.CredUsername), Auth: []gossh.AuthMethod{gossh.Password(password)}, HostKeyCallback: gossh.FixedHostKey(signer.PublicKey()), HostKeyAlgorithms: []string{signer.PublicKey().Type()}})
		if connection != nil {
			_ = connection.Close()
		}
		return err
	}
	oldDone := make(chan error, 1)
	go func() { oldDone <- connect(oldPassword) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old credential did not reach authenticated demand boundary")
	}
	rotated := user
	rotated.Credentials = map[string]any{core.CredUsername: core.CredString(user.Credentials, core.CredUsername), core.CredPassword: "rotated-test-password"}
	inbound.Users = []core.User{rotated}
	if err := engine.Reconcile(t.Context(), []core.Instance{inbound}); err != nil {
		t.Fatal(err)
	}
	descriptor.CredentialGeneration, descriptor.PlacementRevision = 2, 2
	if err := broker.Replace(2, []admission.Descriptor{descriptor}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	close(release)
	request, err := broker.Next(ctx)
	if request != nil {
		_ = broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: request.ClientId})
	}
	if !errors.Is(err, context.DeadlineExceeded) || request != nil {
		t.Fatalf("old credential proof adopted the replacement descriptor: %v", err)
	}
	if err := <-oldDone; err == nil {
		t.Fatal("old credential demand returned an authenticated connection")
	}
	if err := engine.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	newProof, err := broker.Bind([]admission.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	create(newProof)
	newDone := make(chan error, 1)
	go func() { newDone <- connect("rotated-test-password") }()
	next, nextCancel := context.WithTimeout(t.Context(), time.Second)
	defer nextCancel()
	request, err = broker.Next(next)
	if err != nil || request.CredentialGeneration != 2 || request.PlacementRevision != 2 || request.DeploymentRevision != 2 || request.ClientId != user.Email || request.InboundInstanceId != descriptor.InboundInstanceID {
		t.Fatalf("new proof did not retain its signed scope: %+v %v", request, err)
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: request.ClientId}); err != nil {
		t.Fatal(err)
	}
	if err := <-newDone; err == nil {
		t.Fatal("master receipt granted SSH forwarding authority")
	}
	if err := connect(oldPassword); err == nil {
		t.Fatal("rotated old password authenticated")
	}
	check, checkCancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer checkCancel()
	if request, err := broker.Next(check); request != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("revoked old password produced fresh demand")
	}
}
