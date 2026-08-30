package tgbot

import (
	"os"
	"strings"
	"testing"
)

/*
Every bot message is parsed as HTML, and exactly one value in the package was
escaped.

SendMsgToTgbot and editMessageTgBot both set ParseMode "HTML", so Telegram
rejects the WHOLE message when the markup does not parse. A client email, an
inbound remark or a comment holding a '<' therefore silenced the server-usage
report, the inbound list and the depleted-client alert outright — the send
fails, the error goes to the log, and the admin simply stops hearing from the
bot about that client. Nothing on the panel shows it.

The values are escaped where they enter a message rather than at each of the 127
call sites, and the templates' own <b> and <code> are untouched: escaping the
rendered message would show the operator the tags. F291.
*/
func TestBotParamsAreEscapedButTemplatesAreNot(t *testing.T) {
	cases := []struct {
		name  string
		param string
		want  string
	}{
		{
			name:  "an email that opens a tag",
			param: "Email==alice<b>@example.com",
			want:  "Email==alice&lt;b&gt;@example.com",
		},
		{
			name:  "a remark with an ampersand",
			param: "Remark==Frankfurt & Berlin",
			want:  "Remark==Frankfurt &amp; Berlin",
		},
		{
			name:  "a value carrying the separator itself",
			param: "Note==a==b",
			want:  "Note==a==b",
		},
		{
			name:  "an ordinary value is left alone",
			param: "Time==2026-08-30 12:00:00",
			want:  "Time==2026-08-30 12:00:00",
		},
		{
			name:  "something that is not a parameter at all",
			param: "no-separator-here",
			want:  "no-separator-here",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeBotParams([]string{tc.param})
			if got[0] != tc.want {
				t.Fatalf("escapeBotParams(%q) = %q, want %q", tc.param, got[0], tc.want)
			}
		})
	}

	if got := escapeBotParams(nil); got != nil {
		t.Fatalf("escapeBotParams(nil) = %#v, want nil", got)
	}
}

// And it must not double-escape: a call site that escaped its own value would
// now show the operator "&amp;lt;" where their own text was.
func TestNoCallSiteEscapesItsOwnValue(t *testing.T) {
	sources := []string{"tgbot.go", "tgbot_router.go", "tgbot_send.go", "tgbot_report.go"}
	for _, name := range sources {
		body := readBotSource(t, name)
		for _, line := range strings.Split(body, "\n") {
			if !strings.Contains(line, "html.EscapeString(") {
				continue
			}
			if strings.Contains(line, "escaped[i] = name") {
				continue
			}
			t.Errorf("%s escapes a value at the call site as well as at the boundary, so the operator's own text comes back as &amp;lt;:\n  %s", name, strings.TrimSpace(line))
		}
	}
	// The boundary itself must still be there.
	if !strings.Contains(readBotSource(t, "tgbot.go"), "html.EscapeString(value)") {
		t.Fatal("the escape at the I18nBot boundary is gone; this guard is now vacuous")
	}
}

func readBotSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
