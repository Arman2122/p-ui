//go:build linux

package awg

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

/*
Clearing a header has to reach the device, and it cannot do it as zero.

The encoder substitutes WireGuard's own 1/2/3/4 for an unset header when the
panel is the authority. This drives that through the real module and reads the
device back with awg-tools, which stops printing headers entirely once they are
the defaults — so "no h1 line" is the module's own way of saying the inbound is
back to plain WireGuard framing.
*/
func TestClearingAHeaderReturnsTheDeviceToPlainWireguard(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("configuring a device needs root")
	}
	if _, err := exec.LookPath("awg"); err != nil {
		t.Skip("the awg tools are not installed")
	}

	const dev = "awgclr0"
	_ = exec.Command("ip", "link", "del", "dev", dev).Run()
	if out, err := exec.Command("ip", "link", "add", "dev", dev, "type", LinkType).CombinedOutput(); err != nil {
		t.Skipf("creating %s: %v (%s)", dev, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "dev", dev).Run() })

	custom := Params{H1: HeaderRange(10, 10), H2: HeaderRange(20, 20), H3: HeaderRange(30, 30), H4: HeaderRange(40, 40)}
	if err := ConfigureParams(dev, custom); err != nil {
		t.Fatalf("applying custom headers: %v", err)
	}
	if shown := awgShow(t, dev); !strings.Contains(shown, "h1: 10") {
		t.Fatalf("the custom header did not reach the device:\n%s", shown)
	}

	// The whole point: the same struct with the headers dropped.
	if err := ConfigureParams(dev, Params{}); err != nil {
		t.Fatalf("clearing the headers: %v", err)
	}
	shown := awgShow(t, dev)
	for _, name := range []string{"h1:", "h2:", "h3:", "h4:"} {
		if strings.Contains(shown, name) {
			t.Fatalf("%s is still set after a clear, so the device keeps framing every client cannot read:\n%s", name, shown)
		}
	}
}
