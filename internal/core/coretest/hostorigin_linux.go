//go:build linux

package coretest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

/*
An origin that records WHERE each request came from, for an exit's proof on a
host that is also its own provider. Reachability proves nothing there:
everything the provider reaches, the host reaches directly. The source address
the origin sees is the evidence -- the tunnel address on a socket routed through
the exit, the veth's on one that was not.

The origin lives in its own network namespace rather than on a loopback alias,
deliberately: a local address is answered by the kernel's local table, which is
consulted before any fwmark rule, so a marked socket to one never reaches the
egress table at all.
*/

// NamespacedOrigin is an HTTP origin behind a veth, with the log it writes each
// request's client address to.
type NamespacedOrigin struct {
	Addr string
	log  string
}

/*
OriginInNamespace runs the origin in namespace ns, joined to the host by the
veth pair veth0/veth1 addressed inside net24 (given as "10.250.2"). The host
forwards to it, so a request through a provider arrives from its tunnel address
and a direct one from the veth.
*/
func OriginInNamespace(t *testing.T, ns, veth, net24 string) *NamespacedOrigin {
	t.Helper()
	ctx := context.Background()
	host, peer := veth+"0", veth+"1"
	shell("ip netns del " + ns + " 2>/dev/null; ip link del " + host + " 2>/dev/null; true")
	for _, step := range [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", host, "type", "veth", "peer", "name", peer},
		{"ip", "link", "set", peer, "netns", ns},
		{"ip", "addr", "add", net24 + ".1/30", "dev", host},
		{"ip", "link", "set", host, "up"},
		{"ip", "netns", "exec", ns, "ip", "addr", "add", net24 + ".2/30", "dev", peer},
		{"ip", "netns", "exec", ns, "ip", "link", "set", peer, "up"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"},
		{"ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", net24 + ".1"},
		{"sysctl", "-qw", "net.ipv4.ip_forward=1"},
		{"sysctl", "-qw", "net.ipv4.conf." + host + ".rp_filter=2"},
	} {
		if out, err := exec.CommandContext(ctx, step[0], step[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	t.Cleanup(func() { shell("ip netns del " + ns + " 2>/dev/null; ip link del " + host + " 2>/dev/null; true") })
	logPath := filepath.Join(t.TempDir(), "origin.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	server := exec.CommandContext(ctx, "ip", "netns", "exec", ns, "python3", "-m", "http.server", "8080", "--bind", net24+".2")
	server.Stdout, server.Stderr = logFile, logFile
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-server.Process.Pid, syscall.SIGTERM); _ = server.Wait(); _ = logFile.Close() })
	o := &NamespacedOrigin{Addr: net24 + ".2:8080", log: logPath}
	until(t, 10*time.Second, func() bool {
		dialCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", o.Addr)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}, func() string { return "the origin never listened:\n" + tail(logPath) })
	return o
}

/*
SourceSeenWithMark GETs the origin from a socket carrying mark (0 for none) and
returns the address the origin logged the request from. The socket's source is
chosen by the route the mark selects, so the answer is which way the packet left.
*/
func (o *NamespacedOrigin) SourceSeenWithMark(t *testing.T, mark uint32) string {
	t.Helper()
	before, _ := os.ReadFile(o.log)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if mark != 0 {
		dialer.Control = markControl(mark)
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}}
	token := fmt.Sprintf("/mark-%d-%d", mark, time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+o.Addr+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET with mark %#x: %v", mark, err)
	}
	_ = resp.Body.Close()
	var seen string
	until(t, 5*time.Second, func() bool {
		body, _ := os.ReadFile(o.log)
		for _, line := range strings.Split(strings.TrimPrefix(string(body), string(before)), "\n") {
			if strings.Contains(line, token) {
				seen = strings.Fields(line)[0]
				return true
			}
		}
		return false
	}, func() string { return "the origin never logged " + token + ":\n" + tail(o.log) })
	return seen
}

/*
AllowLocalSources lets this host accept, on any interface, a packet whose source
is one of its own addresses, and puts the setting back afterwards.

Needed only because the provider and the exit share this host in a test: what
the provider decrypts carries the exit's tunnel address, which the host itself
holds on the exit's tun, and fib_validate_source drops exactly that as a martian
unless accept_local is set. A provider on another machine never sees this.
*/
func AllowLocalSources(t *testing.T) {
	t.Helper()
	const knob = "net.ipv4.conf.all.accept_local"
	ctx := context.Background()
	previous, err := exec.CommandContext(ctx, "sysctl", "-n", knob).Output()
	if err != nil {
		t.Fatalf("read %s: %v", knob, err)
	}
	if out, err := exec.CommandContext(ctx, "sysctl", "-qw", knob+"=1").CombinedOutput(); err != nil {
		t.Fatalf("set %s: %v\n%s", knob, err, out)
	}
	was := strings.TrimSpace(string(previous))
	t.Cleanup(func() { _ = exec.CommandContext(ctx, "sysctl", "-qw", knob+"="+was).Run() })
}

func shell(script string) string {
	out, _ := exec.CommandContext(context.Background(), "sh", "-c", script).CombinedOutput()
	return string(out)
}

func tail(path string) string {
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}

func until(t *testing.T, d time.Duration, cond func() bool, why func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal(why())
}
