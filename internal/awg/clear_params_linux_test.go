package awg

import (
	"os/exec"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
The claim against the module itself: a cleared parameter reaches the device.

The encoder test above proves the message carries the zero. Only the module
proves the device then drops the value — and that clearing one parameter leaves
the ones the message does not mention exactly where they were, which is what
makes an authoritative write safe here.
*/
func TestModuleClearsAParameterWrittenAsZero(t *testing.T) {
	client := requireModule(t)
	const name = "awgclear0"
	makeDevice(t, name)

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := client.ConfigureDevice(name, Config{
		PrivateKey:    &key,
		Params:        Params{Jc: 4, Jmin: 40, Jmax: 70, S1: 20, S2: 30},
		ReplaceParams: true,
	}); err != nil {
		t.Fatalf("apply the preset: %v", err)
	}
	before := awgShow(t, name)
	for _, want := range []string{"jc: 4", "s1: 20", "s2: 30"} {
		if !strings.Contains(before, want) {
			t.Fatalf("the preset did not land (%q missing); the rest proves nothing:\n%s", want, before)
		}
	}

	// The operator clears s1 and s2 in the form and saves.
	if err := client.ConfigureDevice(name, Config{
		Params:        Params{Jc: 4, Jmin: 40, Jmax: 70},
		ReplaceParams: true,
	}); err != nil {
		t.Fatalf("apply the cleared parameters: %v", err)
	}

	after := awgShow(t, name)
	for _, gone := range []string{"s1: 20", "s2: 30"} {
		if strings.Contains(after, gone) {
			t.Errorf("%q survived the clear: the device still pads and no client can handshake\n%s", gone, after)
		}
	}
	for _, kept := range []string{"jc: 4", "jmin: 40", "jmax: 70"} {
		if !strings.Contains(after, kept) {
			t.Errorf("clearing s1/s2 also lost %q, which the operator did not touch\n%s", kept, after)
		}
	}
}

func awgShow(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("awg", "show", name).CombinedOutput()
	if err != nil {
		t.Skipf("awg tools unavailable to read the device back: %v", err)
	}
	return string(out)
}
