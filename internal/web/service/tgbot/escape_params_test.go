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

/*
The add-client draft was written in Markdown and sent as HTML.

Every delivery path sets ParseMode "HTML", and this message emitted *emphasis*
and `code spans`. In HTML mode those render literally, so the admin saw the
asterisks and backticks. The interpolated values -- the email, the attached
inbounds' remarks, the comment -- were unescaped besides, so a '<' in any of them
made Telegram refuse the whole message and the draft never appeared: the admin
pressed a button in the add-client flow and nothing came back. F298.
*/
func TestTheClientDraftIsHtmlAndEscapesTheOperatorsText(t *testing.T) {
	body := readBotSource(t, "tgbot_client.go")

	start := strings.Index(body, "func (t *Tgbot) BuildClientDraftMessage(")
	if start < 0 {
		t.Fatal("BuildClientDraftMessage was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of BuildClientDraftMessage; this guard is now vacuous")
	}
	draft := body[start : start+end]

	for _, markdown := range []string{"*New client draft*", "`%s`"} {
		if strings.Contains(draft, markdown) {
			t.Errorf("the draft still emits Telegram Markdown (%s) while every send sets ParseMode HTML, so it renders literally", markdown)
		}
	}
	if !strings.Contains(draft, "<b>New client draft</b>") || !strings.Contains(draft, "<code>%s</code>") {
		t.Error("the draft does not use the HTML markup the ParseMode expects")
	}
	for _, value := range []string{"draft.email", "attached", "tgID", "comment"} {
		if !strings.Contains(draft, "html.EscapeString("+value+")") {
			t.Errorf("%s reaches the message unescaped; one '<' in it and Telegram refuses the whole draft", value)
		}
	}
}

/*
/clearall must not send one Telegram message per client.

It sent a separate message for every email on the box. Telegram rate-limits a
chat to a handful of messages a second and answers 429 with a flood-wait, whose
text matches none of the three keywords SendMsgToTgbot retries on -- so on any
panel with real client numbers the tail of the run was dropped, while the resets
themselves went through. The admin saw the first few, then silence, and had no
way to tell a stalled run from a finished one.

One message now, and SendMsgToTgbot already splits a long body into paced
chunks, so the failures ride along in it. F296.
*/
func TestResetAllTrafficSendsOneMessage(t *testing.T) {
	body := readBotSource(t, "tgbot_router.go")

	start := strings.Index(body, `case "reset_all_traffics_c":`)
	if start < 0 {
		t.Fatal("the reset-all handler was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n\tcase ")
	if end < 0 {
		t.Fatal("cannot find the end of the reset-all handler; this guard is now vacuous")
	}
	handler := body[start : start+end]

	loop := strings.Index(handler, "for _, email := range emails {")
	if loop < 0 {
		t.Fatal("the per-email loop is gone; this guard is now vacuous")
	}
	loopEnd := strings.Index(handler[loop:], "\n\t\t}")
	if loopEnd < 0 {
		t.Fatal("cannot find the end of the per-email loop")
	}
	if strings.Contains(handler[loop:loop+loopEnd], "SendMsgToTgbot(") {
		t.Error("the handler still sends inside the per-email loop; Telegram drops the tail of that run and the 429 it answers with is not one of the errors SendMsgToTgbot retries")
	}
	if n := strings.Count(handler, "t.SendMsgToTgbot("); n > 2 {
		t.Errorf("%d sends in the reset-all handler; one summary and one error path is the whole of it", n)
	}
}

/*
A client's own buttons must work when their email is long.

encodeQuery replaces a callback payload over 64 characters with an MD5, which is
how the panel fits a long email into Telegram's limit. answerCallback decoded it
only inside the isAdmin branch; everything below matched the RAW data — the
self-callback gate and the three client_* prefixes alike.

So for a non-admin whose email pushed the payload past 64 characters, the hash
matched no prefix, the gate returned, and nothing answered the callback: the
button span and then stopped, with no message and nothing in the log. The
thresholds are an email over 47 characters for client_sub_links and over 40 for
client_individual_links — long, but ordinary for a
firstname.lastname@company.example.com address.

Decoded once for both roles now, before anything reads it. F295.
*/
func TestTheCallbackPayloadIsDecodedForEveryone(t *testing.T) {
	body := readBotSource(t, "tgbot_router.go")

	start := strings.Index(body, "func (t *Tgbot) answerCallback(")
	if start < 0 {
		t.Fatal("answerCallback was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of answerCallback; this guard is now vacuous")
	}
	handler := body[start : start+end]

	decode := strings.Index(handler, "t.decodeQuery(callbackQuery.Data)")
	adminBranch := strings.Index(handler, "\tif isAdmin {")
	if decode < 0 || adminBranch < 0 {
		t.Fatal("the decode or the admin branch is gone; this guard is now vacuous")
	}
	if decode > adminBranch {
		t.Error("the payload is decoded inside the admin branch, so a non-admin's hashed callback is matched raw and their own buttons do nothing")
	}
	if strings.Count(handler, "callbackQuery.Data") != 1 {
		t.Error("something below the decode still reads the raw payload; for a long email that value is an MD5 and matches none of the prefixes")
	}
	if !strings.Contains(handler, "isClientSelfCallback(decodedQuery)") {
		t.Error("the non-admin gate still matches the raw payload")
	}
}
