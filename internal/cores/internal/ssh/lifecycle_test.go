package ssh

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
stop() snapshotted the connections while a login could still be mid-handshake;
the login then completed on a stopped server. On TCP, wg.Wait blocked for the
life of that client under Core.mu — no core was billed, no cap enforced, no
daemon supervised, and shutdown hung. The late login is closed instead.
*/
func TestAStopDuringALoginEndsTheLogin(t *testing.T) {
	entered, release, hold := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	admit := func(context.Context, core.Admission) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	keys := &memoryHostKey{pem: testHostKeyPEM(t)}
	c := New(Deps{LoadHostKey: keys.load, StoreHostKey: keys.store, Admit: admit})
	// A capped user is the one whose login asks the panel, mid-handshake.
	capped := userN(0)
	capped.LimitIP = 2
	inst := core.Instance{ID: 7, Kind: Kind, Tag: "ssh-7", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{"transport":"tcp"}`, Users: []core.User{capped}}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	go func() {
		if client, err := dialTCP(t, inst, "user0", "secret-0"); err == nil {
			<-hold
			_ = client.Close()
		}
	}()
	t.Cleanup(func() { close(hold) })
	<-entered

	stopped := make(chan struct{})
	go func() {
		_ = c.StopAll(context.Background())
		close(stopped)
	}()
	// Release the login only once stop() has closed the listener, which is
	// after the point where it snapshots the connections it will close.
	addr := net.JoinHostPort(inst.Listen, itoa(inst.Port))
	for deadline := time.Now().Add(5 * time.Second); ; {
		probe, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			break
		}
		_ = probe.Close()
		if time.Now().After(deadline) {
			t.Fatal("the listener never closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll did not return: a login that completed after the stop is being served on a stopped server, under Core.mu")
	}
}

// ClientConfig.Timeout covers the TCP connect alone: a peer that accepts and
// never speaks held maintain() forever, and the next edit of that exit hung
// the core under Core.mu.
func TestAStalledRemoteDoesNotPinTheExit(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	var held []net.Conn
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			held = append(held, conn)
		}
	}()
	previous := exitDialTimeout
	exitDialTimeout = 300 * time.Millisecond
	t.Cleanup(func() { exitDialTimeout = previous })
	ex := &exitClient{id: 1, settings: ExitSettings{Host: "127.0.0.1", Port: silent.Addr().(*net.TCPAddr).Port, Username: "u", Password: "p", HostKeyFingerprint: "SHA256:x"}, dial: (&net.Dialer{}).DialContext}
	done := make(chan error, 1)
	go func() {
		_, err := ex.connect()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("connect() succeeded against a peer that never spoke")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connect() to a peer that accepts and never speaks did not return; the exit is pinned")
	}
}

// A listener that could not bind after a rebuild was stopped, deleted and
// then absent from Health, so the Cores page showed nothing wrong.
func TestAListenerThatCannotBindIsReportedFailed(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	keys := &memoryHostKey{pem: testHostKeyPEM(t)}
	c := New(Deps{LoadHostKey: keys.load, StoreHostKey: keys.store})
	inst := core.Instance{ID: 7, Kind: Kind, Tag: "ssh-7", Listen: "127.0.0.1", Port: taken.Addr().(*net.TCPAddr).Port, Enable: true, Settings: `{"transport":"tcp"}`, Users: []core.User{userN(0)}}
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err == nil {
		t.Fatal("Reconcile bound a port another listener holds")
	}
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Instances) != 1 || h.Instances[0].InstanceID != 7 || h.Instances[0].State != core.HealthFailed || !strings.Contains(h.Instances[0].Detail, "listen") {
		t.Fatalf("Health = %+v; want inbound 7 failed, with the bind error", h.Instances)
	}
	_ = taken.Close()
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("Reconcile with the port free: %v", err)
	}
	h, _ = c.Health(context.Background())
	if len(h.Instances) != 1 || h.Instances[0].State != core.HealthRunning {
		t.Fatalf("Health after the port freed = %+v; want inbound 7 running and the failure gone", h.Instances)
	}
}
