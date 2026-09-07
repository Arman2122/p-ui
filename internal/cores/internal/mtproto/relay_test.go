package mtproto

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// relayInstance is the rig's instance with the WEB relay wanted, every port
// fixed so the rendered [web] table names sockets the stand-in can bind.
func relayInstance(r *rig) core.Instance {
	inst := r.instance(1)
	inst.Settings = `{"throttleMaxConnections":128,"webEnable":true,"webHost":"web.example.com","webCoverDir":"/srv/site",` +
		`"webBackendPort":24701,"webBindPort":24702,"webStatsPort":24703}`
	return inst
}

// relayAlive asks the kernel about the last relay the stand-in spawned.
func (r *rig) relayAlive() bool {
	data, err := os.ReadFile(r.relayPids)
	if err != nil {
		return false
	}
	pids := strings.Fields(string(data))
	if len(pids) == 0 {
		return false
	}
	pid, err := strconv.Atoi(pids[len(pids)-1])
	if err != nil {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

func (r *rig) relaySpawns() int {
	data, err := os.ReadFile(r.relayPids)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		r.t.Fatalf("read relay pid file: %v", err)
	}
	return len(strings.Fields(string(data)))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

/*
The relay is a second process the panel owns beside the proxy: started when the
inbound wants it, counted in the inbound's health, and ended when the inbound
stops wanting it. A client edit reaches it without a restart of either process,
and turning it off is a structural change that restarts the proxy alone.
*/
func TestTheRelayRunsBesideTheProxy(t *testing.T) {
	requirePosixHost(t)
	r := newRig(t)
	c := New(Deps{})
	inst := relayInstance(r)

	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r.waitReady()
	waitFor(t, "the relay to start", func() bool { return r.relaySpawns() == 1 && r.relayAlive() })

	health, err := c.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(health.Instances) != 1 || health.Instances[0].State != core.HealthRunning || !health.Instances[0].Ready {
		t.Fatalf("a proxy with its relay up is one running, ready instance, got %+v", health.Instances)
	}

	// A client edit is a hot apply: neither process restarts, and the relay
	// learns the new client from the file it watches, with a signal for speed.
	added := core.User{Email: "z@example.com", Enable: true, Credentials: map[string]any{CredSecret: "eeff"}}
	if err := c.AddUser(t.Context(), inst, added); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if got := r.starts(); got != 1 {
		t.Fatalf("a client edit restarted the proxy (%d spawns)", got)
	}
	if got := r.relaySpawns(); got != 1 || !r.relayAlive() {
		t.Fatalf("a client edit restarted or ended the relay (%d spawns, alive %v)", got, r.relayAlive())
	}

	// Killing the relay behind the panel's back is repaired by the next pass.
	data, _ := os.ReadFile(r.relayPids)
	pid, _ := strconv.Atoi(strings.Fields(string(data))[0])
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
	waitFor(t, "the relay to die", func() bool { return !r.relayAlive() })
	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile after the relay died: %v", err)
	}
	waitFor(t, "the relay to be restarted", func() bool { return r.relaySpawns() == 2 && r.relayAlive() })
	if got := r.starts(); got != 1 {
		t.Fatalf("repairing the relay restarted the proxy (%d spawns)", got)
	}

	// Turning the relay off is structural: the proxy restarts, the relay ends.
	off := inst
	off.Settings = `{"throttleMaxConnections":128,"webBackendPort":24701}`
	if err := c.Reconcile(t.Context(), []core.Instance{off}); err != nil {
		t.Fatalf("reconcile without the relay: %v", err)
	}
	waitFor(t, "the relay to end", func() bool { return !r.relayAlive() })
	if got := r.starts(); got != 2 {
		t.Fatalf("dropping the relay is a structural change and must restart the proxy once, got %d spawns", got)
	}
	if got := r.relaySpawns(); got != 2 {
		t.Fatalf("no relay may be started for an inbound that stopped wanting one, got %d spawns", got)
	}
}
