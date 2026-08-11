package job

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
			// keeps the space before the separator. Trimmed here rather than fixed:
			// the shipped filters are the contract and the user tag only names a ban.
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

func TestFail2BanProbe(t *testing.T) {
	t.Run("disabled by env, the client is never executed", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "false")
		marker := fakeFail2BanClient(t)
		if fail2BanIsInstalled() {
			t.Fatal("fail2ban should be unavailable when PUI_ENABLE_FAIL2BAN=false")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("fail2ban-client should not have been executed, stat error: %v", err)
		}
	})

	t.Run("an empty env value is not enabled", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "")
		marker := fakeFail2BanClient(t)
		if fail2BanIsInstalled() {
			t.Fatal("fail2ban should be unavailable when PUI_ENABLE_FAIL2BAN is empty")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("fail2ban-client should not have been executed, stat error: %v", err)
		}
	})

	t.Run("enabled by env, the client is probed", func(t *testing.T) {
		t.Setenv("PUI_ENABLE_FAIL2BAN", "true")
		marker := fakeFail2BanClient(t)
		if !fail2BanIsInstalled() {
			t.Fatal("fail2ban should be available when the client probe succeeds")
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("fail2ban-client should have been executed: %v", err)
		}
	})

	t.Run("unset defaults to enabled", func(t *testing.T) {
		value, ok := os.LookupEnv("PUI_ENABLE_FAIL2BAN")
		os.Unsetenv("PUI_ENABLE_FAIL2BAN")
		t.Cleanup(func() {
			if ok {
				os.Setenv("PUI_ENABLE_FAIL2BAN", value)
			} else {
				os.Unsetenv("PUI_ENABLE_FAIL2BAN")
			}
		})
		if !isFail2BanEnabled() {
			t.Fatal("fail2ban should default to enabled when PUI_ENABLE_FAIL2BAN is unset")
		}
	})
}

func fakeFail2BanClient(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-called")
	fakeClient := filepath.Join(dir, "fail2ban-client")
	script := "#!/bin/sh\n: > \"$FAIL2BAN_PROBE_MARKER\"\nexit 0\n"
	if err := os.WriteFile(fakeClient, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fail2ban-client: %v", err)
	}

	t.Setenv("FAIL2BAN_PROBE_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}
