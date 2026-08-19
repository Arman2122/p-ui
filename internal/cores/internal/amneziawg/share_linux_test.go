//go:build linux

package amneziawg

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
)

/*
The config the panel hands out must be one a real AmneziaWG client accepts.

A unit test can only prove the file says what this package meant it to say. It
cannot catch a key the tools spell differently, a range in a format the parser
rejects, or a value the kernel refuses -- and each of those produces a client
that silently fails to connect rather than an error anybody sees. So the
generated file goes to awg setconf against a real device.
*/
func TestGeneratedConfigIsAcceptedByTheTools(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("configuring a device needs root")
	}
	if _, err := exec.LookPath("awg"); err != nil {
		t.Skip("the awg tools are not installed")
	}

	raw, err := exec.Command("awg", "genkey").Output()
	if err != nil {
		t.Skipf("awg genkey: %v", err)
	}
	private := strings.TrimSpace(string(raw))

	settings, err := json.Marshal(map[string]any{
		"secretKey": private,
		"dns":       "9.9.9.9",
		"awg": awg.Params{
			Jc: 4, Jmin: 40, Jmax: 70, S1: 20, S2: 30,
			H1: awg.HeaderRange(10, 19), H2: awg.HeaderRange(20, 29),
			H3: awg.HeaderRange(30, 39), H4: awg.HeaderRange(40, 49),
			RekeyAfterTime: awg.TimerRange(100, 140),
			RandomTrailers: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	share, err := New().RenderClient(
		core.Instance{ID: 3, Kind: Kind, Tag: "awg-in", Port: 51820, Settings: string(settings)},
		core.User{Email: "alice@example.com", Credentials: map[string]any{
			core.CredPrivateKey: private,
			core.CredAllowedIPs: []any{"10.8.0.4/32"},
		}},
		// An address literal, not a hostname: setconf RESOLVES the endpoint, so a
		// documentation name fails here for DNS reasons rather than config ones.
		"203.0.113.9",
	)
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}

	const device = "awgconf0"
	if out, err := exec.Command("ip", "link", "add", "dev", device, "type", "amneziawg").CombinedOutput(); err != nil {
		t.Skipf("creating the device: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "dev", device).Run() })

	// Address, DNS and MTU are wg-quick directives setconf does not parse, so
	// they come out exactly as wg-quick strips them before handing over the
	// rest. Done here rather than by shelling out, so what is under test is this
	// package's output and nothing else.
	var kept []string
	for _, line := range strings.Split(share.Body, "\n") {
		switch strings.TrimSpace(strings.SplitN(line, "=", 2)[0]) {
		case "Address", "DNS", "MTU":
			continue
		}
		kept = append(kept, line)
	}
	stripped := strings.Join(kept, "\n")

	path := filepath.Join(t.TempDir(), "stripped.conf")
	if err := os.WriteFile(path, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("awg", "setconf", device, path).CombinedOutput(); err != nil {
		t.Fatalf("the tools refused the generated config: %v\n%s\n--- config ---\n%s", err, out, stripped)
	}

	// Read back, so a parameter silently dropped on the way in still fails.
	out, err := exec.Command("awg", "show", device).CombinedOutput()
	if err != nil {
		t.Fatalf("awg show: %v (%s)", err, out)
	}
	for _, want := range []string{"jc: 4", "jmin: 40", "jmax: 70"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the device does not report %q after setconf:\n%s", want, out)
		}
	}
}
