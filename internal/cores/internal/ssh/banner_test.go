package ssh

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
)

// A refused login said "permission denied" whatever the reason, so a disabled
// or expired client, or one over its device cap, went to the operator for an
// answer the app could have shown. A wrong password stays bare on purpose.
func TestARefusedLoginTellsTheClientWhy(t *testing.T) {
	disabled := userN(0)
	disabled.Enable = false
	_, inst := startCore(t, `{"transport":"tcp"}`, disabled, userN(1))
	dial := func(user, password string) string {
		var banner string
		cfg := clientConfig(user, password)
		cfg.BannerCallback = func(message string) error { banner = message; return nil }
		if client, err := gossh.Dial("tcp", net.JoinHostPort(inst.Listen, itoa(inst.Port)), cfg); err == nil {
			client.Close()
			t.Fatalf("%s logged in", user)
		}
		return banner
	}
	if got := dial("user0", "secret-0"); !strings.Contains(got, "disabled") {
		t.Fatalf("a disabled client was told %q; want the reason", got)
	}
	if got := dial("user1", "wrong"); got != "" {
		t.Fatalf("a wrong password was told %q; nothing may tell a guesser the account exists", got)
	}
}

// Every session was dropped for a banner edit: the banner was part of the
// listener's fingerprint, so a changed greeting rebuilt the listener.
func TestABannerEditKeepsEverySession(t *testing.T) {
	c, inst := startCore(t, `{"transport":"tcp","banner":"one"}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	before := inst
	inst.Settings = `{"transport":"tcp","banner":"two"}`
	if got := c.PlanChange(before, inst); got != core.ActionHotApply {
		t.Fatalf("PlanChange = %v for a banner edit, want a hot apply", got)
	}
	c.mu.Lock()
	starts := c.starts
	c.mu.Unlock()
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	restarted := c.starts != starts
	c.mu.Unlock()
	if restarted {
		t.Fatal("a banner edit restarted the listener, dropping every session")
	}
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("the session opened before the edit is gone: %v", err)
	}
	defer sess.Close()
	out, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	line := make(chan string, 1)
	go func() {
		got, _ := bufio.NewReader(out).ReadString('\n')
		line <- got
	}()
	select {
	case got := <-line:
		if strings.TrimSpace(got) != "two" {
			t.Fatalf("banner = %q, want the edited one on the surviving session", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no banner within 5s")
	}
}
