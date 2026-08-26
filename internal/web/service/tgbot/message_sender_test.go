package tgbot

import (
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

/*
A message with no sender must not kill the panel.

`Message.From` is `*User` in the Bot API and documented optional — "may be empty
for messages sent to channels" — while `CallbackQuery.From` is a value and can
never be nil. Every command handler here dereferenced it. Nothing guarded it:
telego's `AnyCommand()` checks only that the message exists, and this file's own
`isCommandForCurrentBot` reads only the text.

The consequence is not a broken bot. The dereference runs inside a bare
`go func()` with no recover, so it takes the process down — and the same update
is redelivered on the next long poll, so it is a crash loop, not one crash.
F299.
*/

func TestAMessageWithNoSenderIsNotAnswered(t *testing.T) {
	// Reaching the /start arm at all is the point: it is the first arm that
	// dereferences the sender, and it does so before anything is sent.
	(&Tgbot{}).answerCommand(&telego.Message{
		Chat: telego.Chat{ID: 42},
		Text: "/start",
	}, 42, false)
}

func TestMessageSenderReportsAbsence(t *testing.T) {
	cases := []struct {
		name    string
		message *telego.Message
		wantOK  bool
		wantID  int64
	}{
		{"nil message", nil, false, 0},
		{"channel post, no sender", &telego.Message{Chat: telego.Chat{ID: 1}}, false, 0},
		{"an ordinary user", &telego.Message{From: &telego.User{ID: 7, FirstName: "A"}}, true, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := messageSender(c.message)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if got.ID != c.wantID {
				t.Fatalf("id = %d, want %d", got.ID, c.wantID)
			}
		})
	}
}

/*
And the handlers themselves refuse a senderless message before their bodies run.

Asserted on the source for the same reason the draft-sender guard beside it is:
these are closures registered against a live bot handler group, and standing one
up would test telego rather than the guard. The property is "a registration
whose body reads message.From also declares AnyMessageWithFrom", and that is
what the registration list says.
*/
func TestEveryHandlerThatReadsTheSenderDeclaresIt(t *testing.T) {
	body := readRouterSource(t)

	const marker = "h.HandleMessage(func(ctx *th.Context, message telego.Message) error {"
	registrations := strings.Split(body, marker)
	if len(registrations) < 2 {
		t.Fatal("no message handlers are registered here any more; this guard is now vacuous")
	}

	guarded := 0
	for i, block := range registrations[1:] {
		// The closure body ends at the dedented brace, and the predicate list
		// rides on that same line.
		brace := strings.Index(block, "\n\t\t}, ")
		if brace < 0 {
			t.Fatalf("handler %d has no recognisable end; this guard is now vacuous", i)
		}
		body, tail := block[:brace], block[brace+1:]
		predicates := tail
		if nl := strings.Index(tail, "\n"); nl >= 0 {
			predicates = tail[:nl]
		}
		if !strings.Contains(body, "message.From") {
			continue
		}
		guarded++
		if !strings.Contains(predicates, "th.AnyMessageWithFrom()") {
			t.Errorf("handler %d dereferences message.From but does not declare th.AnyMessageWithFrom(); a channel post reaches it and panics the process. Predicates were: %s",
				i, strings.TrimSpace(predicates))
		}
	}
	if guarded == 0 {
		t.Fatal("no handler reads message.From any more; this guard is now vacuous")
	}
}
