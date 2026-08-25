package job

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/web/service"
)

// repoRoot walks up to the module root so the test reads the installer that
// actually ships rather than a copy of the regex kept in sync by hand.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory; cannot find the installer")
		}
		dir = parent
	}
}

// installerFailregex lifts the failregex create_iplimit_jails writes into
// filter.d/p-ui-ipl.conf on every box this panel has ever been installed on.
func installerFailregex(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "p-ui.sh"))
	if err != nil {
		t.Fatalf("read p-ui.sh: %v", err)
	}
	found := regexp.MustCompile(`(?m)^failregex\s*=\s*(.+)$`).FindSubmatch(body)
	if found == nil {
		t.Fatal("p-ui.sh no longer writes a failregex line; this contract test is certifying nothing")
	}
	return strings.TrimSpace(string(found[1]))
}

/*
fail2banToGo turns a fail2ban failregex into one Go's regexp can run.

<ADDR> and <F-USER> are fail2ban's own tags, expanded by its filter engine into
the capture groups that name the banned host and the offending user. Everything
else in the expression is ordinary regex and is left exactly as written.
*/
func fail2banToGo(t *testing.T, failregex string) *regexp.Regexp {
	t.Helper()
	expanded := strings.NewReplacer(
		"<F-USER>", "(?P<user>",
		"</F-USER>", ")",
		"<ADDR>", `(?P<host>[0-9A-Fa-f:.]+)`,
	).Replace(failregex)
	compiled, err := regexp.Compile(expanded)
	if err != nil {
		t.Fatalf("the installer's failregex does not compile after expanding fail2ban's tags: %v\n%s", err, expanded)
	}
	return compiled
}

/*
TestIPLimitLogLineMatchesTheInstallerRegex pins deployed infrastructure.

The format string is not a log message: every installed box already carries a
fail2ban filter built from p-ui.sh's failregex, so a reformat that still reads
fine to a human silently disables IP limits everywhere in the field. The
assertion is on the MATCH, so a rewording the filter still accepts is allowed.
*/
func TestIPLimitLogLineMatchesTheInstallerRegex(t *testing.T) {
	matcher := fail2banToGo(t, installerFailregex(t))

	cases := []struct {
		name  string
		email string
		ip    string
		unix  int64
	}{
		{"an ipv4 address", "alice", "203.0.113.7", 1786398189},
		{"an ipv6 address", "bob", "2001:db8::1", 1786398190},
		{"an email address as the client name", "carol@example.test", "198.51.100.4", 1786398191},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := fmt.Sprintf(IPLimitLogFormat, tc.email, tc.ip, tc.unix)
			groups := matcher.FindStringSubmatch(line)
			if groups == nil {
				t.Fatalf("the emitted line is not matched by the jail every installed box already runs:\nline:  %s\nregex: %s",
					line, matcher)
			}
			if got := groups[matcher.SubexpIndex("host")]; got != tc.ip {
				t.Errorf("fail2ban would ban %q, the client connected from %q", got, tc.ip)
			}
			// The installer's <F-USER>.+</F-USER> is greedy, so fail2ban's own capture
			// keeps the trailing space. The shipped filter is the contract, not this.
			if got := strings.TrimSpace(groups[matcher.SubexpIndex("user")]); got != tc.email {
				t.Errorf("fail2ban would attribute the ban to %q, want %q", got, tc.email)
			}
		})
	}
}

// TestIPLimitLogTimestampIsSeconds pins the unit. The persisted blob and every
// node sync carry unix seconds, and a millisecond value reads as the year 58000.
func TestIPLimitLogTimestampIsSeconds(t *testing.T) {
	line := fmt.Sprintf(IPLimitLogFormat, "alice", "203.0.113.7", int64(1786398189000)/1000)
	if !strings.HasSuffix(line, "Timestamp = 1786398189") {
		t.Fatalf("the ban line must carry unix seconds, got %q", line)
	}
}

/*
The enforce gate must ask whether a ban would land, not whether Python is
installed.

`fail2ban-client -h` is answered by the client's own argparse. It stays green
with the daemon stopped and with p-ui's three jail files deleted — which the
panel's own menu offers as option 1. On such a box the UI showed the cap
enabled, the job logged addresses over their cap, and nothing was dropped.
*/
func TestFail2BanProbe(t *testing.T) {
	t.Run("disabled by env, the client is never executed", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "false")
		probe := fakeFail2BanClient(t, jailRunning)
		if fail2BanCanEnforce() {
			t.Fatal("fail2ban should be unavailable when PUI_ENABLE_FAIL2BAN=false")
		}
		if probe.called() {
			t.Fatal("fail2ban-client should not have been executed")
		}
	})

	t.Run("an empty env value is not enabled", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "")
		probe := fakeFail2BanClient(t, jailRunning)
		if fail2BanCanEnforce() {
			t.Fatal("fail2ban should be unavailable when PUI_ENABLE_FAIL2BAN is empty")
		}
		if probe.called() {
			t.Fatal("fail2ban-client should not have been executed")
		}
	})

	t.Run("enabled and the jail is loaded", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "true")
		probe := fakeFail2BanClient(t, jailRunning)
		if !fail2BanCanEnforce() {
			t.Fatal("fail2ban should be usable when the jail answers")
		}
		if argv := probe.argv(); !strings.Contains(argv, "status p-ui-ipl") {
			t.Fatalf("the probe must ask the daemon about the jail, got %q", argv)
		}
	})

	// The H2 case: the binary is there and the jail is not.
	t.Run("enabled, client present, jail gone", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "true")
		probe := fakeFail2BanClient(t, jailMissing)
		if fail2BanCanEnforce() {
			t.Fatal("a deleted jail must disable enforcement: -h answers, but no packet is dropped")
		}
		if argv := probe.argv(); !strings.Contains(argv, "status p-ui-ipl") {
			t.Fatalf("the probe must ask the daemon about the jail, got %q", argv)
		}
	})

	t.Run("unset defaults to enabled", func(t *testing.T) {
		os.Unsetenv("PUI_ENABLE_FAIL2BAN")
		probe := fakeFail2BanClient(t, jailRunning)
		if !fail2BanCanEnforce() {
			t.Fatal("fail2ban should default to enabled when the var is unset")
		}
		if !probe.called() {
			t.Fatal("fail2ban-client should have been executed")
		}
	})
}

const (
	jailRunning = false
	jailMissing = true
)

type fail2banProbe struct {
	t          *testing.T
	markerPath string
	argvPath   string
}

func (p fail2banProbe) called() bool {
	_, err := os.Stat(p.markerPath)
	return err == nil
}

func (p fail2banProbe) argv() string {
	p.t.Helper()
	b, err := os.ReadFile(p.argvPath)
	if err != nil {
		return ""
	}
	return string(b)
}

// fakeFail2BanClient puts a stand-in client on PATH that records its argv and,
// when jailMissing, fails the jail query while still answering -h.
func fakeFail2BanClient(t *testing.T, jailMissing bool) fail2banProbe {
	t.Helper()
	service.ResetFail2banJailProbeCache()
	t.Cleanup(service.ResetFail2banJailProbeCache)

	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-called")
	argvFile := filepath.Join(dir, "probe-argv")
	fakeClient := filepath.Join(dir, "fail2ban-client")
	script := `#!/bin/sh
: > "$FAIL2BAN_PROBE_MARKER"
echo "$@" >> "$FAIL2BAN_PROBE_ARGV"
[ "$1" = status ] && exit "$FAIL2BAN_JAIL_EXIT"
exit 0
`
	if err := os.WriteFile(fakeClient, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fail2ban-client: %v", err)
	}

	jailExit := "0"
	if jailMissing {
		jailExit = "1"
	}
	t.Setenv("FAIL2BAN_JAIL_EXIT", jailExit)
	t.Setenv("FAIL2BAN_PROBE_MARKER", marker)
	t.Setenv("FAIL2BAN_PROBE_ARGV", argvFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fail2banProbe{t: t, markerPath: marker, argvPath: argvFile}
}
