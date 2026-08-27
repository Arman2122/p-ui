package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
A prompt the scripts cannot answer must not become an infinite loop.

`while [[ -z "$server_ip" ]]; do read -rp ... done` has no exit for a `read`
that fails. None of the three scripts sets `set -e`, so with stdin at EOF the
read returns non-zero at once, server_ip stays empty and the loop spins.

That is the panel's own updater: panel.go runs update.sh detached under
systemd-run with no tty. The panel is already restarted by then and keeps
serving, so nothing looks broken -- while the update process pins a core, the
status file is never finalised so the UI shows an update permanently in flight,
the EXIT trap never removes the staged script, and config_after_update never
reaches setup_fail2ban.

The campaign added a `-t 0` guard to the SSL prompt twenty lines below one of
these loops, with a comment describing this exact hazard, and left the loops
alone. F152.
*/
func TestNoScriptSpinsOnAPromptItCannotAnswer(t *testing.T) {
	const prompt = `read -rp "Please enter your server's public IPv4 address: " server_ip`

	checked := 0
	for _, name := range []string{"install.sh", "update.sh", "p-ui.sh"} {
		text := readRepoScript(t, name)
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, prompt) {
				continue
			}
			checked++
			if !strings.Contains(line, "|| break") {
				t.Errorf("%s: a server_ip prompt has no exit for a failed read, so it spins forever at EOF:\n  %s", name, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no server_ip prompt found in any script; this guard is now vacuous")
	}

	// And the one the panel's own updater reaches must not even prompt headless.
	update := readRepoScript(t, "update.sh")
	start := strings.Index(update, "Could not auto-detect server IP from any provider")
	if start < 0 {
		t.Fatal("config_after_update no longer reports a failed IP detection; this half of the guard is now vacuous")
	}
	block := update[start:]
	block = block[:strings.Index(block, "done")]
	if !strings.Contains(block, "while [[ -t 0 &&") {
		t.Error("config_after_update prompts for the server IP without testing for a tty; the panel runs this detached under systemd-run")
	}
}

func readRepoScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
